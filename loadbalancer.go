package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/yaml.v3"
)

// Metrics represents collected metrics
type Metrics struct {
	mu sync.RWMutex

	// Request metrics
	TotalRequests     uint64            `json:"total_requests"`
	RequestsPerSecond float64           `json:"requests_per_second"`
	ResponseTime      map[string]uint64 `json:"response_time"` // status code -> count
	ErrorCount        uint64            `json:"error_count"`

	// Backend metrics
	BackendHealth   map[string]bool   `json:"backend_health"`
	BackendRequests map[string]uint64 `json:"backend_requests"`
	BackendErrors   map[string]uint64 `json:"backend_errors"`
	BackendLatency  map[string]uint64 `json:"backend_latency"` // backend -> total latency

	// System metrics
	ActiveConnections uint64  `json:"active_connections"`
	MemoryUsage       uint64  `json:"memory_usage"`
	CPUUsage          float64 `json:"cpu_usage"`

	// Timestamps
	LastUpdated time.Time `json:"last_updated"`
	StartTime   time.Time `json:"start_time"`
}

// Config represents the load balancer configuration
type Config struct {
	Server struct {
		Port        string `yaml:"port"`
		WorkerCount string `yaml:"worker_count"`
		TLS         struct {
			Enabled      string   `yaml:"enabled"`
			CertFile     string   `yaml:"cert_file"`
			KeyFile      string   `yaml:"key_file"`
			MinVersion   string   `yaml:"min_version"`
			CipherSuites []string `yaml:"cipher_suites"`
			ClientAuth   string   `yaml:"client_auth"`
		} `yaml:"tls"`
		LoadBalancing struct {
			Strategy string `yaml:"strategy"`
			IPHash   struct {
				Enabled        string `yaml:"enabled"`
				Header         string `yaml:"header"`
				FallbackHeader string `yaml:"fallback_header"`
			} `yaml:"ip_hash"`
			WeightedRoundRobin struct {
				Enabled          string `yaml:"enabled"`
				WeightByCapacity string `yaml:"weight_by_capacity"`
			} `yaml:"weighted_round_robin"`
		} `yaml:"load_balancing"`
		ConnectionPool struct {
			Enabled               string `yaml:"enabled"`
			MaxIdleConns          string `yaml:"max_idle_conns"`
			MaxIdleConnsPerHost   string `yaml:"max_idle_conns_per_host"`
			MaxConnsPerHost       string `yaml:"max_conns_per_host"`
			IdleConnTimeout       string `yaml:"idle_conn_timeout"`
			MaxLifetime           string `yaml:"max_lifetime"`
			KeepAlive             string `yaml:"keep_alive"`
			DialTimeout           string `yaml:"dial_timeout"`
			TLSHandshakeTimeout   string `yaml:"tls_handshake_timeout"`
			ExpectContinueTimeout string `yaml:"expect_continue_timeout"`
			ResponseHeaderTimeout string `yaml:"response_header_timeout"`
			DisableKeepAlives     bool   `yaml:"disable_keep_alives"`
			DisableCompression    bool   `yaml:"disable_compression"`
		} `yaml:"connection_pool"`
		Cache struct {
			Enabled      string   `yaml:"enabled"`
			TTL          string   `yaml:"ttl"`
			MaxSize      string   `yaml:"max_size"`
			Headers      []string `yaml:"headers"`
			ExcludePaths []string `yaml:"exclude_paths"`
		} `yaml:"cache"`
		CircuitBreaker struct {
			Enabled          string `yaml:"enabled"`
			FailureThreshold string `yaml:"failure_threshold"`
			SuccessThreshold string `yaml:"success_threshold"`
			ResetTimeout     string `yaml:"reset_timeout"`
			HalfOpenTimeout  string `yaml:"half_open_timeout"`
		} `yaml:"circuit_breaker"`
	} `yaml:"server"`
	Metrics struct {
		Enabled string `yaml:"enabled"`
		Port    string `yaml:"port"`
		Path    string `yaml:"path"`
		Auth    struct {
			Enabled     bool   `yaml:"enabled"`
			BearerToken string `yaml:"bearer_token"`
		} `yaml:"auth"`
		CollectInterval string            `yaml:"collect_interval"`
		RetentionPeriod string            `yaml:"retention_period"`
		Labels          map[string]string `yaml:"labels"`
		Prometheus      struct {
			Enabled   string `yaml:"enabled"`
			Namespace string `yaml:"namespace"`
			Subsystem string `yaml:"subsystem"`
			Path      string `yaml:"path"`
		} `yaml:"prometheus"`
	} `yaml:"metrics"`
	Backends []struct {
		URL            string `yaml:"url"`
		Weight         string `yaml:"weight"`
		MaxConnections string `yaml:"max_connections"`
		Healthy        bool   `yaml:"healthy"`
	} `yaml:"backends"`
	Logging struct {
		Enabled    string `yaml:"enabled"`
		Level      string `yaml:"level"`
		File       string `yaml:"file"`
		MaxSize    string `yaml:"max_size"`
		MaxBackups string `yaml:"max_backups"`
		MaxAge     string `yaml:"max_age"`
		Compress   string `yaml:"compress"`
		Format     string `yaml:"format"`
	} `yaml:"logging"`
}

// LogEntry represents a structured log entry
type LogEntry struct {
	Timestamp string      `json:"timestamp"`
	Level     string      `json:"level"`
	Message   string      `json:"message"`
	RequestID string      `json:"request_id,omitempty"`
	Backend   string      `json:"backend,omitempty"`
	Duration  string      `json:"duration,omitempty"`
	Error     string      `json:"error,omitempty"`
	Data      interface{} `json:"data,omitempty"`
}

// Backend represents a backend server
type Backend struct {
	url            *url.URL
	proxy          *httputil.ReverseProxy
	healthy        bool
	lastUsed       time.Time
	weight         string
	maxConnections string
	currentWeight  int
	activeConns    int32
	transport      *http.Transport
	circuitBreaker *CircuitBreaker
}

// LoadBalancer represents a load balancer instance
type LoadBalancer struct {
	backends       []*Backend
	currentBackend uint32
	mu             sync.RWMutex
	server         *http.Server
	metricsServer  *http.Server
	stopChan       chan struct{}
	workerCount    int
	requestChan    chan *http.Request
	responseChan   chan *http.Response
	workerWg       sync.WaitGroup
	logger         *log.Logger
	logFile        *os.File
	config         *Config
	metrics        *Metrics
	ipHashCache    sync.Map
	cache          *Cache
	prometheus     *PrometheusMetrics
	configPath     string
}

// BackendConfig represents a backend configuration
type BackendConfig struct {
	URL            string `yaml:"url"`
	Weight         string `yaml:"weight"`
	MaxConnections string `yaml:"max_connections"`
}

// CircuitBreaker represents a circuit breaker for a backend
type CircuitBreaker struct {
	mu               sync.RWMutex
	failures         string
	successes        string
	lastFailure      time.Time
	state            string // "closed", "open", "half-open"
	resetTimeout     time.Duration
	halfOpenTimeout  time.Duration
	failureThreshold string
	successThreshold string
}

// CacheEntry represents a cached response
type CacheEntry struct {
	Response   []byte
	Headers    http.Header
	StatusCode int
	Expires    time.Time
}

// Cache represents the response cache
type Cache struct {
	mu      sync.RWMutex
	entries map[string]*CacheEntry
	maxSize string
	ttl     time.Duration
}

// PrometheusMetrics represents Prometheus metrics
type PrometheusMetrics struct {
	requestsTotal        *prometheus.CounterVec
	requestDuration      *prometheus.HistogramVec
	backendRequestsTotal *prometheus.CounterVec
	backendErrorsTotal   *prometheus.CounterVec
	backendLatency       *prometheus.HistogramVec
	circuitBreakerState  *prometheus.GaugeVec
	cacheHitsTotal       *prometheus.CounterVec
	cacheMissesTotal     *prometheus.CounterVec
	cacheSize            prometheus.Gauge
	activeConnections    prometheus.Gauge
}

// getBoolValue converts a string value to bool
func getBoolValue(s string) bool {
	return s == "true" || s == "on" || s == "1"
}

// getIntValue converts a string value to int
func getIntValue(s string) int {
	i, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return i
}

// loadConfig loads the configuration from a YAML file
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %v", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("error parsing config file: %v", err)
	}

	return &config, nil
}

// setupLogging configures the logger based on the configuration
func (lb *LoadBalancer) setupLogging() error {
	if !getBoolValue(lb.config.Logging.Enabled) {
		lb.logger = log.New(os.Stdout, "", log.LstdFlags)
		return nil
	}

	// Create log directory if it doesn't exist
	logDir := filepath.Dir(lb.config.Logging.File)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("error creating log directory: %v", err)
	}

	// Open log file
	logFile, err := os.OpenFile(lb.config.Logging.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("error opening log file: %v", err)
	}

	// Create multi-writer to write to both file and stdout
	multiWriter := io.MultiWriter(os.Stdout, logFile)
	lb.logger = log.New(multiWriter, "", 0)
	lb.logFile = logFile

	return nil
}

// log writes a structured log entry
func (lb *LoadBalancer) log(level, message string, data interface{}) {
	if lb.config.Logging.Format == "json" {
		entry := LogEntry{
			Timestamp: time.Now().Format(time.RFC3339),
			Level:     level,
			Message:   message,
			Data:      data,
		}
		if jsonData, err := json.Marshal(entry); err == nil {
			lb.logger.Println(string(jsonData))
		}
	} else {
		lb.logger.Printf("[%s] %s %v", level, message, data)
	}
}

// NewMetrics creates a new Metrics instance
func NewMetrics() *Metrics {
	return &Metrics{
		ResponseTime:    make(map[string]uint64),
		BackendHealth:   make(map[string]bool),
		BackendRequests: make(map[string]uint64),
		BackendErrors:   make(map[string]uint64),
		BackendLatency:  make(map[string]uint64),
		StartTime:       time.Now(),
	}
}

// collectMetrics periodically collects system metrics
func (lb *LoadBalancer) collectMetrics() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-lb.stopChan:
			return
		case <-ticker.C:
			lb.metrics.mu.Lock()
			lb.metrics.LastUpdated = time.Now()
			// Update system metrics here
			lb.metrics.mu.Unlock()
		}
	}
}

// metricsHandler handles metrics requests
func (lb *LoadBalancer) metricsHandler(w http.ResponseWriter, r *http.Request) {
	// Check bearer token if enabled
	if lb.config.Metrics.Auth.Enabled {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		if token != lb.config.Metrics.Auth.BearerToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// Get metrics
	lb.metrics.mu.RLock()
	defer lb.metrics.mu.RUnlock()

	// Calculate requests per second
	duration := time.Since(lb.metrics.StartTime).Seconds()
	if duration > 0 {
		lb.metrics.RequestsPerSecond = float64(lb.metrics.TotalRequests) / duration
	}

	// Return metrics as JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(lb.metrics)
}

// parseDuration parses a duration string with error handling
func parseDuration(duration string) (time.Duration, error) {
	if duration == "" {
		return 0, nil
	}
	return time.ParseDuration(duration)
}

// createTransport creates a new HTTP transport with connection pooling
func (lb *LoadBalancer) createTransport(backendURL *url.URL) (*http.Transport, error) {
	if !getBoolValue(lb.config.Server.ConnectionPool.Enabled) {
		return &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}, nil
	}

	// Parse durations
	idleConnTimeout, err := parseDuration(lb.config.Server.ConnectionPool.IdleConnTimeout)
	if err != nil {
		return nil, fmt.Errorf("invalid idle_conn_timeout: %v", err)
	}

	maxLifetime, err := parseDuration(lb.config.Server.ConnectionPool.MaxLifetime)
	if err != nil {
		return nil, fmt.Errorf("invalid max_lifetime: %v", err)
	}

	keepAlive, err := parseDuration(lb.config.Server.ConnectionPool.KeepAlive)
	if err != nil {
		return nil, fmt.Errorf("invalid keep_alive: %v", err)
	}

	dialTimeout, err := parseDuration(lb.config.Server.ConnectionPool.DialTimeout)
	if err != nil {
		return nil, fmt.Errorf("invalid dial_timeout: %v", err)
	}

	tlsHandshakeTimeout, err := parseDuration(lb.config.Server.ConnectionPool.TLSHandshakeTimeout)
	if err != nil {
		return nil, fmt.Errorf("invalid tls_handshake_timeout: %v", err)
	}

	expectContinueTimeout, err := parseDuration(lb.config.Server.ConnectionPool.ExpectContinueTimeout)
	if err != nil {
		return nil, fmt.Errorf("invalid expect_continue_timeout: %v", err)
	}

	responseHeaderTimeout, err := parseDuration(lb.config.Server.ConnectionPool.ResponseHeaderTimeout)
	if err != nil {
		return nil, fmt.Errorf("invalid response_header_timeout: %v", err)
	}

	// Create transport with connection pooling
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: keepAlive,
		}).DialContext,
		MaxIdleConns:          getIntValue(lb.config.Server.ConnectionPool.MaxIdleConns),
		MaxIdleConnsPerHost:   getIntValue(lb.config.Server.ConnectionPool.MaxIdleConnsPerHost),
		MaxConnsPerHost:       getIntValue(lb.config.Server.ConnectionPool.MaxConnsPerHost),
		IdleConnTimeout:       idleConnTimeout,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ExpectContinueTimeout: expectContinueTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
		DisableKeepAlives:     lb.config.Server.ConnectionPool.DisableKeepAlives,
		DisableCompression:    lb.config.Server.ConnectionPool.DisableCompression,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	// Set connection lifetime if specified
	if maxLifetime > 0 {
		transport.MaxConnsPerHost = getIntValue(lb.config.Server.ConnectionPool.MaxConnsPerHost)
		transport.MaxIdleConns = getIntValue(lb.config.Server.ConnectionPool.MaxIdleConns)
		transport.MaxIdleConnsPerHost = getIntValue(lb.config.Server.ConnectionPool.MaxIdleConnsPerHost)
	}

	return transport, nil
}

// NewPrometheusMetrics creates a new PrometheusMetrics instance
func NewPrometheusMetrics(namespace, subsystem string, labels map[string]string) *PrometheusMetrics {
	labelNames := []string{"backend", "method", "path", "status"}
	backendLabelNames := []string{"backend"}

	return &PrometheusMetrics{
		requestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "requests_total",
				Help:      "Total number of requests processed",
			},
			labelNames,
		),
		requestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "request_duration_seconds",
				Help:      "Request duration in seconds",
				Buckets:   prometheus.DefBuckets,
			},
			labelNames,
		),
		backendRequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "backend_requests_total",
				Help:      "Total number of requests to backends",
			},
			backendLabelNames,
		),
		backendErrorsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "backend_errors_total",
				Help:      "Total number of backend errors",
			},
			backendLabelNames,
		),
		backendLatency: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "backend_latency_seconds",
				Help:      "Backend request latency in seconds",
				Buckets:   prometheus.DefBuckets,
			},
			backendLabelNames,
		),
		circuitBreakerState: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "circuit_breaker_state",
				Help:      "Circuit breaker state (0: closed, 1: half-open, 2: open)",
			},
			backendLabelNames,
		),
		cacheHitsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "cache_hits_total",
				Help:      "Total number of cache hits",
			},
			[]string{"path"},
		),
		cacheMissesTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "cache_misses_total",
				Help:      "Total number of cache misses",
			},
			[]string{"path"},
		),
		cacheSize: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "cache_size",
				Help:      "Current size of the response cache",
			},
		),
		activeConnections: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "active_connections",
				Help:      "Number of active connections",
			},
		),
	}
}

// NewCircuitBreaker creates a new CircuitBreaker instance
func NewCircuitBreaker(failureThreshold, successThreshold string, resetTimeout, halfOpenTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:            "closed",
		failureThreshold: failureThreshold,
		successThreshold: successThreshold,
		resetTimeout:     resetTimeout,
		halfOpenTimeout:  halfOpenTimeout,
	}
}

// AllowRequest checks if a request should be allowed based on the circuit breaker state
func (cb *CircuitBreaker) AllowRequest() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	switch cb.state {
	case "closed":
		return true
	case "open":
		if time.Since(cb.lastFailure) > cb.resetTimeout {
			cb.mu.RUnlock()
			cb.mu.Lock()
			cb.state = "half-open"
			cb.mu.Unlock()
			cb.mu.RLock()
			return true
		}
		return false
	case "half-open":
		return true
	default:
		return false
	}
}

// RecordSuccess records a successful request
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == "half-open" {
		successes := getIntValue(cb.successes)
		successes++
		cb.successes = fmt.Sprintf("%d", successes)
		if successes >= getIntValue(cb.successThreshold) {
			cb.state = "closed"
			cb.failures = "0"
			cb.successes = "0"
		}
	}
}

// RecordFailure records a failed request
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	failures := getIntValue(cb.failures)
	failures++
	cb.failures = fmt.Sprintf("%d", failures)
	cb.lastFailure = time.Now()

	if cb.state == "closed" && failures >= getIntValue(cb.failureThreshold) {
		cb.state = "open"
	} else if cb.state == "half-open" {
		cb.state = "open"
		cb.successes = "0"
	}
}

// NewCache creates a new Cache instance
func NewCache(maxSize string, ttl time.Duration) *Cache {
	return &Cache{
		entries: make(map[string]*CacheEntry),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// cleanup periodically removes expired cache entries
func (c *Cache) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for key, entry := range c.entries {
			if now.After(entry.Expires) {
				delete(c.entries, key)
			}
		}
		c.mu.Unlock()
	}
}

// Get retrieves a cached response
func (c *Cache) Get(key string) (*CacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entries[key]
	if !exists || time.Now().After(entry.Expires) {
		return nil, false
	}
	return entry, true
}

// Set stores a response in the cache
func (c *Cache) Set(key string, entry *CacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if we need to evict entries
	if len(c.entries) >= getIntValue(c.maxSize) {
		// Simple LRU: remove oldest entry
		var oldestKey string
		var oldestTime time.Time
		for k, v := range c.entries {
			if oldestTime.IsZero() || v.Expires.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.Expires
			}
		}
		delete(c.entries, oldestKey)
	}

	c.entries[key] = entry
}

// generateCacheKey generates a cache key for a request
func (lb *LoadBalancer) generateCacheKey(r *http.Request) string {
	// Include method, path, and specified headers in the cache key
	key := r.Method + ":" + r.URL.Path

	for _, header := range lb.config.Server.Cache.Headers {
		if value := r.Header.Get(header); value != "" {
			key += ":" + header + "=" + value
		}
	}

	return key
}

// watchConfig watches for configuration changes
func (lb *LoadBalancer) watchConfig() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	lastModTime := time.Time{}
	fileInfo, err := os.Stat(lb.configPath)
	if err == nil {
		lastModTime = fileInfo.ModTime()
	}

	for {
		select {
		case <-lb.stopChan:
			return
		case <-ticker.C:
			fileInfo, err := os.Stat(lb.configPath)
			if err != nil {
				lb.log("error", "Error checking config file", map[string]interface{}{
					"error": err.Error(),
				})
				continue
			}

			if fileInfo.ModTime().After(lastModTime) {
				lb.log("info", "Configuration file changed, reloading", nil)
				newConfig, err := loadConfig(lb.configPath)
				if err != nil {
					lb.log("error", "Error reloading config", map[string]interface{}{
						"error": err.Error(),
					})
					continue
				}

				// Update configuration
				lb.mu.Lock()
				lb.config = newConfig
				lb.mu.Unlock()

				// Update backends
				if err := lb.updateBackends(); err != nil {
					lb.log("error", "Error updating backends", map[string]interface{}{
						"error": err.Error(),
					})
				}

				lastModTime = fileInfo.ModTime()
			}
		}
	}
}

// updateBackends updates the backend list based on the new configuration
func (lb *LoadBalancer) updateBackends() error {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	// Create a map of existing backends
	existingBackends := make(map[string]*Backend)
	for _, backend := range lb.backends {
		existingBackends[backend.url.String()] = backend
	}

	// Create new backend list
	newBackends := make([]*Backend, 0, len(lb.config.Backends))

	for _, backendConfig := range lb.config.Backends {
		backendURL, err := url.Parse(backendConfig.URL)
		if err != nil {
			return fmt.Errorf("invalid backend address %s: %v", backendConfig.URL, err)
		}

		// Check if backend already exists
		if existingBackend, exists := existingBackends[backendURL.String()]; exists {
			// Update existing backend
			existingBackend.weight = backendConfig.Weight
			existingBackend.maxConnections = backendConfig.MaxConnections
			newBackends = append(newBackends, existingBackend)
			delete(existingBackends, backendURL.String())
		} else {
			// Create new backend
			transport, err := lb.createTransport(backendURL)
			if err != nil {
				return fmt.Errorf("failed to create transport for %s: %v", backendConfig.URL, err)
			}

			proxy := httputil.NewSingleHostReverseProxy(backendURL)
			proxy.Transport = transport

			backend := &Backend{
				url:            backendURL,
				proxy:          proxy,
				healthy:        true,
				lastUsed:       time.Now(),
				weight:         backendConfig.Weight,
				maxConnections: backendConfig.MaxConnections,
				transport:      transport,
			}

			if getBoolValue(lb.config.Server.CircuitBreaker.Enabled) {
				resetTimeout, _ := time.ParseDuration(lb.config.Server.CircuitBreaker.ResetTimeout)
				halfOpenTimeout, _ := time.ParseDuration(lb.config.Server.CircuitBreaker.HalfOpenTimeout)
				backend.circuitBreaker = NewCircuitBreaker(
					lb.config.Server.CircuitBreaker.FailureThreshold,
					lb.config.Server.CircuitBreaker.SuccessThreshold,
					resetTimeout,
					halfOpenTimeout,
				)
			}

			newBackends = append(newBackends, backend)
		}
	}

	// Close transports for removed backends
	for _, backend := range existingBackends {
		if backend.transport != nil {
			backend.transport.CloseIdleConnections()
		}
	}

	// Update backend list
	lb.backends = newBackends

	return nil
}

// NewLoadBalancer creates a new load balancer instance
func NewLoadBalancer(configPath string) (*LoadBalancer, error) {
	// Load configuration
	config, err := loadConfig(configPath)
	if err != nil {
		return nil, err
	}

	lb := &LoadBalancer{
		backends:     make([]*Backend, 0, len(config.Backends)),
		stopChan:     make(chan struct{}),
		workerCount:  getIntValue(config.Server.WorkerCount),
		requestChan:  make(chan *http.Request, getIntValue(config.Server.WorkerCount)*2),
		responseChan: make(chan *http.Response, getIntValue(config.Server.WorkerCount)*2),
		config:       config,
		metrics:      NewMetrics(),
		configPath:   configPath,
	}

	// Setup logging
	if err := lb.setupLogging(); err != nil {
		return nil, err
	}

	// Initialize backends
	for _, backendConfig := range config.Backends {
		backendURL, err := url.Parse(backendConfig.URL)
		if err != nil {
			return nil, fmt.Errorf("invalid backend address %s: %v", backendConfig.URL, err)
		}

		// Create transport with connection pooling
		transport, err := lb.createTransport(backendURL)
		if err != nil {
			return nil, fmt.Errorf("failed to create transport for %s: %v", backendConfig.URL, err)
		}

		proxy := httputil.NewSingleHostReverseProxy(backendURL)
		proxy.Transport = transport

		// Customize the proxy's error handling
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			atomic.AddUint64(&lb.metrics.ErrorCount, 1)
			lb.metrics.mu.Lock()
			lb.metrics.BackendErrors[backendURL.Host]++
			lb.metrics.mu.Unlock()

			lb.log("error", "Proxy error", map[string]interface{}{
				"backend": backendURL.Host,
				"error":   err.Error(),
			})
			http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		}

		backend := &Backend{
			url:            backendURL,
			proxy:          proxy,
			healthy:        true,
			lastUsed:       time.Now(),
			weight:         backendConfig.Weight,
			maxConnections: backendConfig.MaxConnections,
			transport:      transport,
		}

		lb.backends = append(lb.backends, backend)

		// Initialize backend metrics
		lb.metrics.mu.Lock()
		lb.metrics.BackendHealth[backendURL.Host] = true
		lb.metrics.BackendRequests[backendURL.Host] = 0
		lb.metrics.BackendErrors[backendURL.Host] = 0
		lb.metrics.BackendLatency[backendURL.Host] = 0
		lb.metrics.mu.Unlock()
	}

	lb.log("info", "Load balancer initialized", map[string]interface{}{
		"backends":     len(lb.backends),
		"worker_count": lb.workerCount,
		"pool_enabled": getBoolValue(lb.config.Server.ConnectionPool.Enabled),
	})

	// Initialize Prometheus metrics if enabled
	if getBoolValue(lb.config.Metrics.Prometheus.Enabled) {
		lb.prometheus = NewPrometheusMetrics(
			lb.config.Metrics.Prometheus.Namespace,
			lb.config.Metrics.Prometheus.Subsystem,
			map[string]string{"environment": "production"}, // Default labels
		)
	}

	// Initialize cache if enabled
	if getBoolValue(lb.config.Server.Cache.Enabled) {
		ttl, err := time.ParseDuration(lb.config.Server.Cache.TTL)
		if err != nil {
			return nil, fmt.Errorf("invalid cache TTL: %v", err)
		}
		lb.cache = NewCache(lb.config.Server.Cache.MaxSize, ttl)
	}

	// Initialize circuit breakers for backends
	for _, backend := range lb.backends {
		if getBoolValue(lb.config.Server.CircuitBreaker.Enabled) {
			resetTimeout, err := time.ParseDuration(lb.config.Server.CircuitBreaker.ResetTimeout)
			if err != nil {
				return nil, fmt.Errorf("invalid circuit breaker reset timeout: %v", err)
			}
			halfOpenTimeout, err := time.ParseDuration(lb.config.Server.CircuitBreaker.HalfOpenTimeout)
			if err != nil {
				return nil, fmt.Errorf("invalid circuit breaker half-open timeout: %v", err)
			}
			backend.circuitBreaker = NewCircuitBreaker(
				lb.config.Server.CircuitBreaker.FailureThreshold,
				lb.config.Server.CircuitBreaker.SuccessThreshold,
				resetTimeout,
				halfOpenTimeout,
			)
		}
	}

	// Start configuration watcher
	go lb.watchConfig()

	return lb, nil
}

// getTLSConfig creates a TLS configuration based on the provided settings
func (lb *LoadBalancer) getTLSConfig() (*tls.Config, error) {
	if !getBoolValue(lb.config.Server.TLS.Enabled) {
		return nil, nil
	}

	// Load certificate and key
	cert, err := tls.LoadX509KeyPair(lb.config.Server.TLS.CertFile, lb.config.Server.TLS.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS certificate: %v", err)
	}

	// Parse minimum TLS version
	var minVersion uint16
	switch strings.ToLower(lb.config.Server.TLS.MinVersion) {
	case "1.2":
		minVersion = tls.VersionTLS12
	case "1.3":
		minVersion = tls.VersionTLS13
	default:
		return nil, fmt.Errorf("unsupported TLS version: %s", lb.config.Server.TLS.MinVersion)
	}

	// Parse cipher suites
	cipherSuites := make([]uint16, 0, len(lb.config.Server.TLS.CipherSuites))
	for _, suite := range lb.config.Server.TLS.CipherSuites {
		switch suite {
		case "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384":
			cipherSuites = append(cipherSuites, tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384)
		case "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384":
			cipherSuites = append(cipherSuites, tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384)
		case "TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305":
			cipherSuites = append(cipherSuites, tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305)
		case "TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305":
			cipherSuites = append(cipherSuites, tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305)
		case "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256":
			cipherSuites = append(cipherSuites, tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256)
		case "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256":
			cipherSuites = append(cipherSuites, tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256)
		default:
			return nil, fmt.Errorf("unsupported cipher suite: %s", suite)
		}
	}

	// Parse client authentication
	var clientAuth tls.ClientAuthType
	switch strings.ToLower(lb.config.Server.TLS.ClientAuth) {
	case "none":
		clientAuth = tls.NoClientCert
	case "request":
		clientAuth = tls.RequestClientCert
	case "require":
		clientAuth = tls.RequireAndVerifyClientCert
	default:
		return nil, fmt.Errorf("unsupported client authentication: %s", lb.config.Server.TLS.ClientAuth)
	}

	return &tls.Config{
		Certificates:             []tls.Certificate{cert},
		MinVersion:               minVersion,
		CipherSuites:             cipherSuites,
		PreferServerCipherSuites: true,
		ClientAuth:               clientAuth,
		CurvePreferences: []tls.CurveID{
			tls.CurveP521,
			tls.CurveP384,
			tls.CurveP256,
		},
	}, nil
}

// Start starts the load balancer
func (lb *LoadBalancer) Start() error {
	// Create TLS config if enabled
	tlsConfig, err := lb.getTLSConfig()
	if err != nil {
		return fmt.Errorf("failed to create TLS config: %v", err)
	}

	// Start main server
	lb.server = &http.Server{
		Addr:      lb.config.Server.Port,
		Handler:   lb,
		TLSConfig: tlsConfig,
	}

	// Start metrics server if enabled
	if getBoolValue(lb.config.Metrics.Enabled) {
		metricsMux := http.NewServeMux()

		// Register regular metrics handler
		metricsMux.HandleFunc(lb.config.Metrics.Path, lb.metricsHandler)

		// Register Prometheus handler if enabled
		if getBoolValue(lb.config.Metrics.Prometheus.Enabled) {
			metricsMux.Handle(lb.config.Metrics.Prometheus.Path, promhttp.Handler())
		}

		lb.metricsServer = &http.Server{
			Addr:    lb.config.Metrics.Port,
			Handler: metricsMux,
		}

		go func() {
			lb.log("info", "Starting metrics server", map[string]interface{}{
				"port": lb.config.Metrics.Port,
				"paths": []string{
					lb.config.Metrics.Path,
					lb.config.Metrics.Prometheus.Path,
				},
			})
			if err := lb.metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				lb.log("error", "Metrics server error", map[string]interface{}{
					"error": err.Error(),
				})
			}
		}()
	}

	// Start worker pool
	for i := 0; i < lb.workerCount; i++ {
		lb.workerWg.Add(1)
		go lb.worker()
	}

	// Start health check goroutine
	go lb.healthCheck()

	// Start metrics collection
	go lb.collectMetrics()

	lb.log("info", "Starting load balancer", map[string]interface{}{
		"port":     lb.config.Server.Port,
		"tls":      lb.config.Server.TLS.Enabled,
		"protocol": "https",
	})

	if getBoolValue(lb.config.Server.TLS.Enabled) {
		return lb.server.ListenAndServeTLS("", "")
	}
	return lb.server.ListenAndServe()
}

// Stop gracefully stops the load balancer
func (lb *LoadBalancer) Stop() error {
	lb.log("info", "Stopping load balancer", nil)
	close(lb.stopChan)
	lb.workerWg.Wait()

	// Close all transports
	for _, backend := range lb.backends {
		if backend.transport != nil {
			backend.transport.CloseIdleConnections()
		}
	}

	// Shutdown metrics server if enabled
	if getBoolValue(lb.config.Metrics.Enabled) && lb.metricsServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := lb.metricsServer.Shutdown(ctx); err != nil {
			lb.log("error", "Error shutting down metrics server", map[string]interface{}{
				"error": err.Error(),
			})
		}
	}

	// Shutdown main server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if lb.logFile != nil {
		lb.logFile.Close()
	}

	return lb.server.Shutdown(ctx)
}

// ServeHTTP handles incoming HTTP requests
func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	requestID := fmt.Sprintf("%d", time.Now().UnixNano())

	// Update metrics
	atomic.AddUint64(&lb.metrics.TotalRequests, 1)
	atomic.AddUint64(&lb.metrics.ActiveConnections, 1)
	defer atomic.AddUint64(&lb.metrics.ActiveConnections, ^uint64(0))

	lb.log("info", "Incoming request", map[string]interface{}{
		"request_id": requestID,
		"method":     r.Method,
		"path":       r.URL.Path,
		"remote":     r.RemoteAddr,
	})

	// Check cache for GET requests
	if getBoolValue(lb.config.Server.Cache.Enabled) && (r.Method == "GET" || r.Method == "HEAD") {
		// Skip caching for excluded paths
		for _, path := range lb.config.Server.Cache.ExcludePaths {
			if strings.HasPrefix(r.URL.Path, path) {
				goto skipCache
			}
		}

		cacheKey := lb.generateCacheKey(r)
		if entry, found := lb.cache.Get(cacheKey); found {
			lb.prometheus.cacheHitsTotal.WithLabelValues(r.URL.Path).Inc()
			// Copy headers
			for key, values := range entry.Headers {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			w.WriteHeader(entry.StatusCode)
			w.Write(entry.Response)
			return
		}
		lb.prometheus.cacheMissesTotal.WithLabelValues(r.URL.Path).Inc()
	}
skipCache:

	// Get backend using the configured strategy
	backend := lb.getNextBackend(r)
	if backend == nil {
		atomic.AddUint64(&lb.metrics.ErrorCount, 1)
		lb.log("error", "No healthy backends available", map[string]interface{}{
			"request_id": requestID,
		})
		http.Error(w, "No healthy backends available", http.StatusServiceUnavailable)
		return
	}

	// Check circuit breaker
	if !backend.circuitBreaker.AllowRequest() {
		atomic.AddUint64(&lb.metrics.ErrorCount, 1)
		lb.log("error", "Circuit breaker open", map[string]interface{}{
			"request_id": requestID,
			"backend":    backend.url.Host,
		})
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	// Increment active connections for the backend
	atomic.AddInt32(&backend.activeConns, 1)
	defer atomic.AddInt32(&backend.activeConns, -1)

	// Update backend metrics
	lb.metrics.mu.Lock()
	lb.metrics.BackendRequests[backend.url.Host]++
	lb.metrics.mu.Unlock()

	// Create a response recorder to capture the response
	recorder := httptest.NewRecorder()

	// Forward the request
	backend.proxy.ServeHTTP(recorder, r)

	// Check if the request was successful
	if recorder.Code >= 200 && recorder.Code < 500 {
		backend.circuitBreaker.RecordSuccess()
	} else {
		backend.circuitBreaker.RecordFailure()
	}

	// Cache the response if it's a GET request
	if getBoolValue(lb.config.Server.Cache.Enabled) && (r.Method == "GET" || r.Method == "HEAD") {
		cacheKey := lb.generateCacheKey(r)
		ttl, _ := time.ParseDuration(lb.config.Server.Cache.TTL) // We already validated this in NewLoadBalancer
		entry := &CacheEntry{
			Response:   recorder.Body.Bytes(),
			Headers:    recorder.Header(),
			StatusCode: recorder.Code,
			Expires:    time.Now().Add(ttl),
		}
		lb.cache.Set(cacheKey, entry)
	}

	// Copy headers and body to the original response writer
	for key, values := range recorder.Header() {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(recorder.Code)
	w.Write(recorder.Body.Bytes())

	// Update Prometheus metrics
	duration := time.Since(start)
	durationSeconds := duration.Seconds()
	lb.prometheus.requestsTotal.WithLabelValues(
		backend.url.Host,
		r.Method,
		r.URL.Path,
		fmt.Sprintf("%d", recorder.Code),
	).Inc()
	lb.prometheus.requestDuration.WithLabelValues(
		backend.url.Host,
		r.Method,
		r.URL.Path,
		fmt.Sprintf("%d", recorder.Code),
	).Observe(durationSeconds)
	lb.prometheus.backendRequestsTotal.WithLabelValues(backend.url.Host).Inc()
	lb.prometheus.backendLatency.WithLabelValues(backend.url.Host).Observe(durationSeconds)

	if recorder.Code >= 500 {
		lb.prometheus.backendErrorsTotal.WithLabelValues(backend.url.Host).Inc()
	}

	// Update circuit breaker state metric
	var stateValue float64
	switch backend.circuitBreaker.state {
	case "closed":
		stateValue = 0
	case "half-open":
		stateValue = 1
	case "open":
		stateValue = 2
	}
	lb.prometheus.circuitBreakerState.WithLabelValues(backend.url.Host).Set(stateValue)

	lb.log("info", "Request completed", map[string]interface{}{
		"request_id": requestID,
		"duration":   duration.String(),
		"backend":    backend.url.Host,
		"status":     recorder.Code,
	})
}

// getClientIP extracts the client IP from the request
func (lb *LoadBalancer) getClientIP(r *http.Request) string {
	// Try X-Forwarded-For header first
	if ip := r.Header.Get(lb.config.Server.LoadBalancing.IPHash.Header); ip != "" {
		// Get the first IP in the chain
		if comma := strings.Index(ip, ","); comma != -1 {
			return strings.TrimSpace(ip[:comma])
		}
		return ip
	}

	// Try fallback header
	if ip := r.Header.Get(lb.config.Server.LoadBalancing.IPHash.FallbackHeader); ip != "" {
		return ip
	}

	// Fallback to remote address
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// hashIP generates a hash for the given IP address
func (lb *LoadBalancer) hashIP(ip string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(ip))
	return h.Sum32()
}

// getBackendByIPHash selects a backend based on the client's IP address
func (lb *LoadBalancer) getBackendByIPHash(r *http.Request) *Backend {
	ip := lb.getClientIP(r)
	hash := lb.hashIP(ip)

	// Check cache first
	if cached, ok := lb.ipHashCache.Load(ip); ok {
		if backend, ok := cached.(*Backend); ok && backend.healthy {
			return backend
		}
	}

	lb.mu.RLock()
	defer lb.mu.RUnlock()

	if len(lb.backends) == 0 {
		return nil
	}

	// Select backend based on hash
	idx := int(hash % uint32(len(lb.backends)))
	backend := lb.backends[idx]

	// Cache the result
	if backend.healthy {
		lb.ipHashCache.Store(ip, backend)
	}

	return backend
}

// getBackendByWeightedRoundRobin selects a backend using weighted round-robin
func (lb *LoadBalancer) getBackendByWeightedRoundRobin() *Backend {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	if len(lb.backends) == 0 {
		return nil
	}

	var totalWeight int
	var selectedBackend *Backend
	var maxWeight int

	// Find the backend with the highest current weight
	for _, backend := range lb.backends {
		if !backend.healthy {
			continue
		}

		// Adjust weight based on capacity if enabled
		weight := getIntValue(backend.weight)
		if getBoolValue(lb.config.Server.LoadBalancing.WeightedRoundRobin.WeightByCapacity) {
			// Reduce weight based on current connections
			connRatio := float64(atomic.LoadInt32(&backend.activeConns)) / float64(getIntValue(backend.maxConnections))
			weight = int(float64(weight) * (1 - connRatio))
		}

		backend.currentWeight += weight
		totalWeight += weight

		if backend.currentWeight > maxWeight {
			maxWeight = backend.currentWeight
			selectedBackend = backend
		}
	}

	if selectedBackend != nil {
		// Decrease the selected backend's weight
		selectedBackend.currentWeight -= totalWeight
	}

	return selectedBackend
}

// getBackendByLeastConnections selects a backend with the least active connections
func (lb *LoadBalancer) getBackendByLeastConnections() *Backend {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	if len(lb.backends) == 0 {
		return nil
	}

	var selectedBackend *Backend
	var minConns int32 = -1

	for _, backend := range lb.backends {
		if !backend.healthy {
			continue
		}

		activeConns := atomic.LoadInt32(&backend.activeConns)
		if minConns == -1 || activeConns < minConns {
			minConns = activeConns
			selectedBackend = backend
		}
	}

	return selectedBackend
}

// getNextBackend selects the next backend based on the configured strategy
func (lb *LoadBalancer) getNextBackend(r *http.Request) *Backend {
	switch lb.config.Server.LoadBalancing.Strategy {
	case "ip_hash":
		if getBoolValue(lb.config.Server.LoadBalancing.IPHash.Enabled) {
			return lb.getBackendByIPHash(r)
		}
		fallthrough
	case "weighted_round_robin":
		if getBoolValue(lb.config.Server.LoadBalancing.WeightedRoundRobin.Enabled) {
			return lb.getBackendByWeightedRoundRobin()
		}
		fallthrough
	case "least_connections":
		return lb.getBackendByLeastConnections()
	default:
		// Fallback to standard round-robin
		return lb.getNextBackendRoundRobin()
	}
}

// getNextBackendRoundRobin implements standard round-robin selection
func (lb *LoadBalancer) getNextBackendRoundRobin() *Backend {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	if len(lb.backends) == 0 {
		return nil
	}

	startIdx := int(atomic.LoadUint32(&lb.currentBackend))
	for i := 0; i < len(lb.backends); i++ {
		idx := (startIdx + i) % len(lb.backends)
		backend := lb.backends[idx]
		if backend.healthy {
			atomic.StoreUint32(&lb.currentBackend, uint32((idx+1)%len(lb.backends)))
			return backend
		}
	}

	return nil
}

// checkBackendHealth checks if a backend is healthy
func (lb *LoadBalancer) checkBackendHealth(backend *Backend) bool {
	// Create a client with timeout
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true, // Don't reuse connections for health checks
		},
	}

	// Try the health endpoint first
	healthURL := fmt.Sprintf("%s/health", backend.url.String())
	resp, err := client.Get(healthURL)
	if err == nil {
		// Read and close the response body
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			lb.log("info", "Backend health check passed via /health endpoint", map[string]interface{}{
				"backend": backend.url.Host,
				"status":  resp.StatusCode,
			})
			return true
		}
		lb.log("info", "Backend /health endpoint returned non-200 status", map[string]interface{}{
			"backend": backend.url.Host,
			"status":  resp.StatusCode,
		})
	} else {
		lb.log("info", "Backend /health endpoint check failed, trying main URL", map[string]interface{}{
			"backend": backend.url.Host,
			"error":   err.Error(),
		})
	}

	// If health endpoint fails, try the main URL
	mainURL := backend.url.String()
	resp, err = client.Get(mainURL)
	if err != nil {
		lb.log("error", "Backend health check failed", map[string]interface{}{
			"backend": backend.url.Host,
			"error":   err.Error(),
		})
		return false
	}

	// Read and close the response body
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// Consider any 2xx or 3xx response as healthy
	isHealthy := resp.StatusCode >= 200 && resp.StatusCode < 400
	lb.log("info", "Backend health check result", map[string]interface{}{
		"backend": backend.url.Host,
		"status":  resp.StatusCode,
		"healthy": isHealthy,
	})
	return isHealthy
}

// healthCheck periodically checks the health of backends
func (lb *LoadBalancer) healthCheck() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-lb.stopChan:
			return
		case <-ticker.C:
			lb.mu.Lock()
			for _, backend := range lb.backends {
				wasHealthy := backend.healthy
				backend.healthy = lb.checkBackendHealth(backend)
				if wasHealthy != backend.healthy {
					lb.log("info", "Backend health status changed", map[string]interface{}{
						"backend": backend.url.Host,
						"status":  backend.healthy,
					})
				}
			}
			lb.mu.Unlock()
		}
	}
}

// worker processes requests from the request channel
func (lb *LoadBalancer) worker() {
	defer lb.workerWg.Done()

	for {
		select {
		case <-lb.stopChan:
			return
		case req := <-lb.requestChan:
			backend := lb.getNextBackend(req)
			if backend == nil {
				lb.log("error", "No healthy backend available for worker", nil)
				continue
			}

			// Update backend's last used time
			backend.lastUsed = time.Now()

			// Create a new request
			newReq, err := http.NewRequest(req.Method, backend.url.String()+req.URL.Path, req.Body)
			if err != nil {
				lb.log("error", "Failed to create new request", map[string]interface{}{
					"error": err.Error(),
				})
				continue
			}

			// Copy headers
			for key, values := range req.Header {
				for _, value := range values {
					newReq.Header.Add(key, value)
				}
			}

			// Make the request
			client := &http.Client{
				Timeout: 10 * time.Second,
			}
			resp, err := client.Do(newReq)
			if err != nil {
				lb.log("error", "Failed to forward request", map[string]interface{}{
					"backend": backend.url.Host,
					"error":   err.Error(),
				})
				continue
			}

			// Send response to channel
			select {
			case lb.responseChan <- resp:
				lb.log("info", "Response forwarded", map[string]interface{}{
					"backend": backend.url.Host,
					"status":  resp.StatusCode,
				})
			default:
				resp.Body.Close()
				lb.log("error", "Response channel full", map[string]interface{}{
					"backend": backend.url.Host,
				})
			}
		}
	}
}
