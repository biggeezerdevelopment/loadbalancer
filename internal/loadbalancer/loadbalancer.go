package loadbalancer

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
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/klauspost/compress/gzip"
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

// Cache represents a response cache
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

// NewLoadBalancer creates a new load balancer instance
func NewLoadBalancer(configPath string) (*LoadBalancer, error) {
	config, err := loadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %v", err)
	}

	lb := &LoadBalancer{
		config:     config,
		configPath: configPath,
		stopChan:   make(chan struct{}),
		metrics:    NewMetrics(),
	}

	if err := lb.setupLogging(); err != nil {
		return nil, fmt.Errorf("failed to setup logging: %v", err)
	}

	if err := lb.updateBackends(); err != nil {
		return nil, fmt.Errorf("failed to update backends: %v", err)
	}

	workerCount := getIntValue(config.Server.WorkerCount)
	if workerCount <= 0 {
		workerCount = 100 // Default to 100 workers
	}
	lb.workerCount = workerCount
	lb.requestChan = make(chan *http.Request, workerCount*2)
	lb.responseChan = make(chan *http.Response, workerCount*2)

	// Initialize cache if enabled
	if getBoolValue(config.Server.Cache.Enabled) {
		ttl, err := parseDuration(config.Server.Cache.TTL)
		if err != nil {
			ttl = 10 * time.Minute // Default TTL
		}
		lb.cache = NewCache(config.Server.Cache.MaxSize, ttl)
	}

	// Start workers
	for i := 0; i < workerCount; i++ {
		lb.workerWg.Add(1)
		go lb.worker()
	}

	// Start health check
	go lb.healthCheck()

	// Start metrics collection
	go lb.collectMetrics()

	// Start config watcher
	go lb.watchConfig()

	return lb, nil
}

// Start starts the load balancer server
func (lb *LoadBalancer) Start() error {
	port := lb.config.Server.Port
	if port == "" {
		port = "8080" // Default port
	}

	lb.log("info", "Starting load balancer", map[string]interface{}{
		"port": port,
		"tls":  getBoolValue(lb.config.Server.TLS.Enabled),
	})

	// Create server
	lb.server = &http.Server{
		Addr:    ":" + port,
		Handler: lb,
	}

	// Setup TLS if enabled
	if getBoolValue(lb.config.Server.TLS.Enabled) {
		tlsConfig, err := lb.getTLSConfig()
		if err != nil {
			lb.log("error", "Failed to setup TLS", err)
			return fmt.Errorf("failed to setup TLS: %v", err)
		}
		lb.server.TLSConfig = tlsConfig
	}

	// Start metrics server if enabled
	if getBoolValue(lb.config.Metrics.Enabled) {
		metricsPort := lb.config.Metrics.Port
		if metricsPort == "" {
			metricsPort = "9091" // Default metrics port
		}

		lb.log("info", "Starting metrics server", map[string]interface{}{
			"port": metricsPort,
		})

		lb.metricsServer = &http.Server{
			Addr:    ":" + metricsPort,
			Handler: promhttp.Handler(),
		}

		go func() {
			if err := lb.metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				lb.log("error", "Metrics server error", err)
			}
		}()
	}

	// Start main server
	lb.log("info", "Starting main server", nil)
	if getBoolValue(lb.config.Server.TLS.Enabled) {
		return lb.server.ListenAndServeTLS("", "")
	}
	return lb.server.ListenAndServe()
}

// Stop stops the load balancer server
func (lb *LoadBalancer) Stop() error {
	close(lb.stopChan)

	// Stop main server
	if lb.server != nil {
		if err := lb.server.Shutdown(context.Background()); err != nil {
			return fmt.Errorf("failed to shutdown server: %v", err)
		}
	}

	// Stop metrics server
	if lb.metricsServer != nil {
		if err := lb.metricsServer.Shutdown(context.Background()); err != nil {
			return fmt.Errorf("failed to shutdown metrics server: %v", err)
		}
	}

	// Wait for workers to finish
	lb.workerWg.Wait()

	// Close log file
	if lb.logFile != nil {
		if err := lb.logFile.Close(); err != nil {
			return fmt.Errorf("failed to close log file: %v", err)
		}
	}

	return nil
}

// ServeHTTP implements the http.Handler interface
func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Log incoming request
	lb.log("info", "Incoming request", map[string]interface{}{
		"method": r.Method,
		"path":   r.URL.Path,
		"ip":     lb.getClientIP(r),
	})

	// Increment total requests
	atomic.AddUint64(&lb.metrics.TotalRequests, 1)

	// Get next backend
	backend := lb.getNextBackend(r)
	if backend == nil {
		lb.log("error", "No healthy backends available", nil)
		http.Error(w, "No healthy backends available", http.StatusServiceUnavailable)
		return
	}

	// Log backend selection
	lb.log("info", "Selected backend", map[string]interface{}{
		"backend": backend.url.String(),
		"healthy": backend.healthy,
	})

	// Check circuit breaker
	if !backend.circuitBreaker.AllowRequest() {
		lb.log("warn", "Circuit breaker open", map[string]interface{}{
			"backend": backend.url.String(),
		})
		http.Error(w, "Circuit breaker open", http.StatusServiceUnavailable)
		return
	}

	// Check cache if enabled
	if getBoolValue(lb.config.Server.Cache.Enabled) {
		cacheKey := lb.generateCacheKey(r)
		if entry, ok := lb.cache.Get(cacheKey); ok {
			lb.log("info", "Cache hit", map[string]interface{}{
				"path": r.URL.Path,
				"key":  cacheKey,
			})
			// Copy headers
			for k, v := range entry.Headers {
				w.Header()[k] = v
			}
			w.WriteHeader(entry.StatusCode)
			w.Write(entry.Response)
			return
		}
		lb.log("info", "Cache miss", map[string]interface{}{
			"path": r.URL.Path,
			"key":  cacheKey,
		})
	}

	// Forward request to backend
	startTime := time.Now()
	backend.proxy.ServeHTTP(w, r)
	duration := time.Since(startTime)

	// Log request completion
	lb.log("info", "Request completed", map[string]interface{}{
		"backend":  backend.url.String(),
		"duration": duration.String(),
	})
}

// getNextBackend returns the next backend to use based on the load balancing strategy
func (lb *LoadBalancer) getNextBackend(r *http.Request) *Backend {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	if len(lb.backends) == 0 {
		return nil
	}

	// Get strategy
	strategy := lb.config.Server.LoadBalancing.Strategy

	// Select backend based on strategy
	switch strategy {
	case "ip_hash":
		if getBoolValue(lb.config.Server.LoadBalancing.IPHash.Enabled) {
			return lb.getBackendByIPHash(r)
		}
	case "weighted_round_robin":
		if getBoolValue(lb.config.Server.LoadBalancing.WeightedRoundRobin.Enabled) {
			return lb.getBackendByWeightedRoundRobin()
		}
	case "least_connections":
		return lb.getBackendByLeastConnections()
	default:
		return lb.getNextBackendRoundRobin()
	}

	return lb.getNextBackendRoundRobin()
}

// getNextBackendRoundRobin returns the next backend using round-robin
func (lb *LoadBalancer) getNextBackendRoundRobin() *Backend {
	// Get next backend index
	next := atomic.AddUint32(&lb.currentBackend, 1) % uint32(len(lb.backends))
	return lb.backends[next]
}

// getBackendByIPHash returns a backend based on IP hash
func (lb *LoadBalancer) getBackendByIPHash(r *http.Request) *Backend {
	ip := lb.getClientIP(r)
	hash := lb.hashIP(ip)
	return lb.backends[hash%uint32(len(lb.backends))]
}

// getBackendByWeightedRoundRobin returns a backend based on weighted round-robin
func (lb *LoadBalancer) getBackendByWeightedRoundRobin() *Backend {
	var totalWeight int
	var selectedBackend *Backend

	// Calculate total weight
	for _, backend := range lb.backends {
		weight := getIntValue(backend.weight)
		if weight <= 0 {
			weight = 1 // Default weight
		}
		totalWeight += weight
	}

	// Select backend
	for _, backend := range lb.backends {
		weight := getIntValue(backend.weight)
		if weight <= 0 {
			weight = 1 // Default weight
		}
		backend.currentWeight += weight
		if selectedBackend == nil || backend.currentWeight > selectedBackend.currentWeight {
			selectedBackend = backend
		}
	}

	// Reset weights
	for _, backend := range lb.backends {
		backend.currentWeight -= totalWeight
	}

	return selectedBackend
}

// getBackendByLeastConnections returns the backend with the least active connections
func (lb *LoadBalancer) getBackendByLeastConnections() *Backend {
	var selectedBackend *Backend
	var minConns int32 = -1

	for _, backend := range lb.backends {
		conns := atomic.LoadInt32(&backend.activeConns)
		if minConns == -1 || conns < minConns {
			minConns = conns
			selectedBackend = backend
		}
	}

	return selectedBackend
}

// getClientIP returns the client IP address
func (lb *LoadBalancer) getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return strings.Split(ip, ",")[0]
	}

	// Check X-Real-IP header
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}

	// Get IP from remote address
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// hashIP hashes an IP address
func (lb *LoadBalancer) hashIP(ip string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(ip))
	return h.Sum32()
}

// checkBackendHealth checks if a backend is healthy
func (lb *LoadBalancer) checkBackendHealth(backend *Backend) bool {
	// Create health check request
	healthURL := backend.url.String() + "/health"
	req, err := http.NewRequest("GET", healthURL, nil)
	if err != nil {
		lb.log("error", "failed to create health check request", err)
		return false
	}

	// Send request
	client := &http.Client{
		Timeout: 5 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		lb.log("error", "health check request failed", err)
		return false
	}
	defer resp.Body.Close()

	// Check response
	return resp.StatusCode == http.StatusOK
}

// healthCheck periodically checks backend health
func (lb *LoadBalancer) healthCheck() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	lb.log("info", "Starting health check", nil)

	for {
		select {
		case <-ticker.C:
			lb.mu.Lock()
			for _, backend := range lb.backends {
				wasHealthy := backend.healthy
				backend.healthy = lb.checkBackendHealth(backend)
				if wasHealthy != backend.healthy {
					lb.log("info", "Backend health changed", map[string]interface{}{
						"backend": backend.url.String(),
						"healthy": backend.healthy,
					})
				}
			}
			lb.mu.Unlock()
		case <-lb.stopChan:
			lb.log("info", "Stopping health check", nil)
			return
		}
	}
}

// worker processes requests
func (lb *LoadBalancer) worker() {
	defer lb.workerWg.Done()

	for {
		select {
		case req := <-lb.requestChan:
			// Process request
			backend := lb.getNextBackend(req)
			if backend == nil {
				continue
			}

			// Forward request
			backend.proxy.ServeHTTP(nil, req)
		case <-lb.stopChan:
			return
		}
	}
}

// loadConfig loads the configuration from a file
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %v", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %v", err)
	}

	return &config, nil
}

// setupLogging sets up logging
func (lb *LoadBalancer) setupLogging() error {
	if !getBoolValue(lb.config.Logging.Enabled) {
		return nil
	}

	// Create log directory if it doesn't exist
	logDir := filepath.Dir(lb.config.Logging.File)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %v", err)
	}

	// Parse max size
	maxSize, err := strconv.ParseInt(lb.config.Logging.MaxSize, 10, 64)
	if err != nil {
		maxSize = 100 // Default to 100MB
	}

	// Parse max backups
	maxBackups, err := strconv.Atoi(lb.config.Logging.MaxBackups)
	if err != nil {
		maxBackups = 3 // Default to 3 backups
	}

	// Parse max age
	maxAge, err := time.ParseDuration(lb.config.Logging.MaxAge)
	if err != nil {
		maxAge = 24 * time.Hour // Default to 24 hours
	}

	// Open log file
	logFile, err := os.OpenFile(lb.config.Logging.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %v", err)
	}

	// Create logger with rotation
	lb.logFile = logFile
	lb.logger = log.New(logFile, "", 0)

	// Start log rotation goroutine
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// Check if rotation is needed
				if fi, err := logFile.Stat(); err == nil {
					if fi.Size() >= maxSize {
						// Close current file
						logFile.Close()

						// Rotate files
						for i := maxBackups - 1; i >= 0; i-- {
							oldPath := lb.config.Logging.File
							if i > 0 {
								oldPath = fmt.Sprintf("%s.%d", lb.config.Logging.File, i)
							}
							newPath := fmt.Sprintf("%s.%d", lb.config.Logging.File, i+1)

							// Remove oldest backup if it exists
							if i == maxBackups-1 {
								os.Remove(newPath)
							}

							// Rename file
							if _, err := os.Stat(oldPath); err == nil {
								os.Rename(oldPath, newPath)
							}

							// Compress if enabled
							if getBoolValue(lb.config.Logging.Compress) {
								go func(path string) {
									if err := compressFile(path); err != nil {
										log.Printf("Failed to compress log file %s: %v", path, err)
									}
								}(newPath)
							}
						}

						// Open new log file
						newFile, err := os.OpenFile(lb.config.Logging.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
						if err != nil {
							log.Printf("Failed to open new log file: %v", err)
							continue
						}

						// Update logger
						lb.logFile = newFile
						lb.logger = log.New(newFile, "", 0)
					}
				}

				// Clean up old log files
				if err := cleanupOldLogs(lb.config.Logging.File, maxBackups, maxAge); err != nil {
					log.Printf("Failed to cleanup old logs: %v", err)
				}

			case <-lb.stopChan:
				return
			}
		}
	}()

	return nil
}

// compressFile compresses a file using gzip
func compressFile(path string) error {
	// Open the file
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	// Create gzip file
	gzFile, err := os.Create(path + ".gz")
	if err != nil {
		return err
	}
	defer gzFile.Close()

	// Create gzip writer
	gzWriter := gzip.NewWriter(gzFile)
	defer gzWriter.Close()

	// Copy file contents to gzip writer
	if _, err := io.Copy(gzWriter, file); err != nil {
		return err
	}

	// Remove original file
	return os.Remove(path)
}

// cleanupOldLogs removes log files older than maxAge
func cleanupOldLogs(basePath string, maxBackups int, maxAge time.Duration) error {
	// Get all log files
	files, err := filepath.Glob(basePath + "*")
	if err != nil {
		return err
	}

	now := time.Now()
	for _, file := range files {
		// Skip current log file
		if file == basePath {
			continue
		}

		// Get file info
		fi, err := os.Stat(file)
		if err != nil {
			continue
		}

		// Check if file is too old
		if now.Sub(fi.ModTime()) > maxAge {
			os.Remove(file)
		}
	}

	return nil
}

// log logs a message
func (lb *LoadBalancer) log(level, message string, data interface{}) {
	if lb.logger == nil {
		return
	}

	entry := LogEntry{
		Timestamp: time.Now().Format(time.RFC3339),
		Level:     level,
		Message:   message,
		Data:      data,
	}

	jsonData, err := json.Marshal(entry)
	if err != nil {
		return
	}

	lb.logger.Println(string(jsonData))
}

// NewMetrics creates a new metrics instance
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

// collectMetrics periodically collects metrics
func (lb *LoadBalancer) collectMetrics() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			lb.metrics.mu.Lock()
			lb.metrics.LastUpdated = time.Now()
			lb.metrics.mu.Unlock()
		case <-lb.stopChan:
			return
		}
	}
}

// metricsHandler handles metrics requests
func (lb *LoadBalancer) metricsHandler(w http.ResponseWriter, r *http.Request) {
	// Check authentication if enabled
	if lb.config.Metrics.Auth.Enabled {
		token := r.Header.Get("Authorization")
		if token != "Bearer "+lb.config.Metrics.Auth.BearerToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// Get metrics
	lb.metrics.mu.RLock()
	metrics := *lb.metrics
	lb.metrics.mu.RUnlock()

	// Encode metrics
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(metrics); err != nil {
		http.Error(w, "Failed to encode metrics", http.StatusInternalServerError)
	}
}

// parseDuration parses a duration string
func parseDuration(duration string) (time.Duration, error) {
	return time.ParseDuration(duration)
}

// createTransport creates a transport for a backend
func (lb *LoadBalancer) createTransport(backendURL *url.URL) (*http.Transport, error) {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	// Configure TLS
	if backendURL.Scheme == "https" {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}

	return transport, nil
}

// NewPrometheusMetrics creates a new Prometheus metrics instance
func NewPrometheusMetrics(namespace, subsystem string, labels map[string]string) *PrometheusMetrics {
	return &PrometheusMetrics{
		requestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "requests_total",
				Help:      "Total number of requests",
			},
			[]string{"method", "path", "status"},
		),
		requestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "request_duration_seconds",
				Help:      "Request duration in seconds",
			},
			[]string{"method", "path"},
		),
		backendRequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "backend_requests_total",
				Help:      "Total number of requests to backends",
			},
			[]string{"backend", "status"},
		),
		backendErrorsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "backend_errors_total",
				Help:      "Total number of errors from backends",
			},
			[]string{"backend"},
		),
		backendLatency: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "backend_latency_seconds",
				Help:      "Backend latency in seconds",
			},
			[]string{"backend"},
		),
		circuitBreakerState: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "circuit_breaker_state",
				Help:      "Circuit breaker state",
			},
			[]string{"backend"},
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
				Help:      "Cache size in bytes",
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

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(failureThreshold, successThreshold string, resetTimeout, halfOpenTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		failureThreshold: failureThreshold,
		successThreshold: successThreshold,
		resetTimeout:     resetTimeout,
		halfOpenTimeout:  halfOpenTimeout,
		state:            "closed",
	}
}

// AllowRequest checks if a request is allowed
func (cb *CircuitBreaker) AllowRequest() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	switch cb.state {
	case "closed":
		return true
	case "open":
		if time.Since(cb.lastFailure) > cb.resetTimeout {
			cb.state = "half-open"
			return true
		}
		return false
	case "half-open":
		return true
	default:
		return true
	}
}

// RecordSuccess records a successful request
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case "half-open":
		successes := getIntValue(cb.successes)
		successes++
		cb.successes = strconv.Itoa(successes)

		if successes >= getIntValue(cb.successThreshold) {
			cb.state = "closed"
			cb.successes = "0"
		}
	}
}

// RecordFailure records a failed request
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case "closed":
		failures := getIntValue(cb.failures)
		failures++
		cb.failures = strconv.Itoa(failures)

		if failures >= getIntValue(cb.failureThreshold) {
			cb.state = "open"
			cb.lastFailure = time.Now()
		}
	case "half-open":
		cb.state = "open"
		cb.lastFailure = time.Now()
	}
}

// NewCache creates a new cache
func NewCache(maxSize string, ttl time.Duration) *Cache {
	cache := &Cache{
		entries: make(map[string]*CacheEntry),
		maxSize: maxSize,
		ttl:     ttl,
	}

	// Start cleanup goroutine
	go cache.cleanup()

	return cache
}

// cleanup periodically cleans up expired entries
func (c *Cache) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
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

// Get gets a value from the cache
func (c *Cache) Get(key string) (*CacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}

	if time.Now().After(entry.Expires) {
		delete(c.entries, key)
		return nil, false
	}

	return entry, true
}

// Set sets a value in the cache
func (c *Cache) Set(key string, entry *CacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check cache size
	maxSize := getIntValue(c.maxSize)
	if maxSize > 0 {
		var totalSize int
		for _, e := range c.entries {
			totalSize += len(e.Response)
		}

		if totalSize+len(entry.Response) > maxSize {
			// Remove oldest entries
			for key, e := range c.entries {
				delete(c.entries, key)
				totalSize -= len(e.Response)
				if totalSize+len(entry.Response) <= maxSize {
					break
				}
			}
		}
	}

	// Set entry
	c.entries[key] = entry
}

// generateCacheKey generates a cache key for a request
func (lb *LoadBalancer) generateCacheKey(r *http.Request) string {
	// Include method and path
	key := r.Method + ":" + r.URL.Path

	// Include query parameters
	if r.URL.RawQuery != "" {
		key += "?" + r.URL.RawQuery
	}

	// Include headers
	for _, header := range lb.config.Server.Cache.Headers {
		if value := r.Header.Get(header); value != "" {
			key += ":" + header + "=" + value
		}
	}

	return key
}

// watchConfig watches for configuration changes
func (lb *LoadBalancer) watchConfig() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Load new configuration
			config, err := loadConfig(lb.configPath)
			if err != nil {
				lb.log("error", "failed to load configuration", err)
				continue
			}

			// Update configuration
			lb.mu.Lock()
			lb.config = config
			lb.mu.Unlock()

			// Update backends
			if err := lb.updateBackends(); err != nil {
				lb.log("error", "failed to update backends", err)
			}
		case <-lb.stopChan:
			return
		}
	}
}

// updateBackends updates the backends
func (lb *LoadBalancer) updateBackends() error {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	// Create new backends
	var newBackends []*Backend
	for _, backendConfig := range lb.config.Backends {
		// Parse URL
		backendURL, err := url.Parse(backendConfig.URL)
		if err != nil {
			return fmt.Errorf("invalid backend URL: %v", err)
		}

		// Create transport
		transport, err := lb.createTransport(backendURL)
		if err != nil {
			return fmt.Errorf("failed to create transport: %v", err)
		}

		// Create reverse proxy
		proxy := &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				req.URL.Scheme = backendURL.Scheme
				req.URL.Host = backendURL.Host
				req.Host = backendURL.Host
			},
			Transport: transport,
		}

		// Create circuit breaker
		circuitBreaker := NewCircuitBreaker(
			lb.config.Server.CircuitBreaker.FailureThreshold,
			lb.config.Server.CircuitBreaker.SuccessThreshold,
			30*time.Second, // Reset timeout
			5*time.Second,  // Half-open timeout
		)

		// Create backend
		backend := &Backend{
			url:            backendURL,
			proxy:          proxy,
			healthy:        backendConfig.Healthy,
			weight:         backendConfig.Weight,
			maxConnections: backendConfig.MaxConnections,
			transport:      transport,
			circuitBreaker: circuitBreaker,
		}

		newBackends = append(newBackends, backend)
	}

	// Update backends
	lb.backends = newBackends

	return nil
}

// getTLSConfig returns the TLS configuration
func (lb *LoadBalancer) getTLSConfig() (*tls.Config, error) {
	// Load certificate
	cert, err := tls.LoadX509KeyPair(
		lb.config.Server.TLS.CertFile,
		lb.config.Server.TLS.KeyFile,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load certificate: %v", err)
	}

	// Create TLS config
	config := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	// Set minimum version
	if lb.config.Server.TLS.MinVersion != "" {
		switch lb.config.Server.TLS.MinVersion {
		case "1.0":
			config.MinVersion = tls.VersionTLS10
		case "1.1":
			config.MinVersion = tls.VersionTLS11
		case "1.2":
			config.MinVersion = tls.VersionTLS12
		case "1.3":
			config.MinVersion = tls.VersionTLS13
		}
	}

	// Set cipher suites
	if len(lb.config.Server.TLS.CipherSuites) > 0 {
		var suites []uint16
		for _, suite := range lb.config.Server.TLS.CipherSuites {
			switch suite {
			case "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256":
				suites = append(suites, tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256)
			case "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256":
				suites = append(suites, tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256)
			case "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384":
				suites = append(suites, tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384)
			case "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384":
				suites = append(suites, tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384)
			case "TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305":
				suites = append(suites, tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305)
			case "TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305":
				suites = append(suites, tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305)
			}
		}
		if len(suites) > 0 {
			config.CipherSuites = suites
		}
	}

	// Set client auth
	if lb.config.Server.TLS.ClientAuth != "" {
		switch lb.config.Server.TLS.ClientAuth {
		case "request":
			config.ClientAuth = tls.RequestClientCert
		case "require":
			config.ClientAuth = tls.RequireAndVerifyClientCert
		case "verify":
			config.ClientAuth = tls.VerifyClientCertIfGiven
		}
	}

	return config, nil
}

// getBoolValue converts a string to a boolean
func getBoolValue(s string) bool {
	return s == "true" || s == "1" || s == "yes" || s == "y"
}

// getIntValue converts a string to an integer
func getIntValue(s string) int {
	i, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return i
}
