package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

// Config represents the complete configuration
type Config struct {
	Server         ServerConfig
	TLS            TLSConfig
	LoadBalancing  LoadBalancingConfig
	ConnectionPool ConnectionPoolConfig
	Cache          CacheConfig
	CircuitBreaker CircuitBreakerConfig
	Metrics        MetricsConfig
	Logging        LoggingConfig
	Backends       []Backend
}

// ServerConfig represents the server configuration
type ServerConfig struct {
	Port           string               `json:"port"`
	WorkerCount    string               `json:"worker_count"`
	TLS            TLSConfig            `json:"tls"`
	LoadBalancing  LoadBalancingConfig  `json:"load_balancing"`
	ConnectionPool ConnectionPoolConfig `json:"connection_pool"`
	Cache          CacheConfig          `json:"cache"`
	CircuitBreaker CircuitBreakerConfig `json:"circuit_breaker"`
}

// TLSConfig represents the TLS configuration
type TLSConfig struct {
	Enabled      string   `json:"enabled"`
	CertFile     string   `json:"cert_file"`
	KeyFile      string   `json:"key_file"`
	MinVersion   string   `json:"min_version"`
	CipherSuites []string `json:"cipher_suites"`
	ClientAuth   string   `json:"client_auth"`
}

type IPHashConfig struct {
	Enabled        string `json:"enabled"`
	Header         string `json:"header"`
	FallbackHeader string `json:"fallback_header"`
}

type WeightedRoundRobinConfig struct {
	Enabled          string `json:"enabled"`
	WeightByCapacity string `json:"weight_by_capacity"`
}

// LoadBalancingConfig represents the load balancing configuration
type LoadBalancingConfig struct {
	Strategy           string                   `json:"strategy"`
	IPHash             IPHashConfig             `json:"ip_hash"`
	WeightedRoundRobin WeightedRoundRobinConfig `json:"weighted_round_robin"`
}

// ConnectionPoolConfig represents the connection pool configuration
type ConnectionPoolConfig struct {
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
	DisableKeepAlives     int    `yaml:"disable_keep_alives"`
	DisableCompression    int    `yaml:"disable_compression"`
}

// CacheConfig represents the cache configuration
type CacheConfig struct {
	Enabled      string   `yaml:"enabled"`
	TTL          string   `yaml:"ttl"`
	MaxSize      string   `yaml:"max_size"`
	Headers      []string `yaml:"headers"`
	Methods      []string `yaml:"methods"`
	StatusCodes  []int    `yaml:"status_codes"`
	ExcludePaths []string `yaml:"exclude_paths"`
}

// CircuitBreakerConfig represents the circuit breaker configuration
type CircuitBreakerConfig struct {
	Enabled          string `yaml:"enabled"`
	FailureThreshold string `yaml:"failure_threshold"`
	SuccessThreshold string `yaml:"success_threshold"`
	ResetTimeout     string `yaml:"reset_timeout"`
	HalfOpenTimeout  string `yaml:"half_open_timeout"`
}

// MetricsConfig represents the metrics configuration
type MetricsConfig struct {
	Enabled         string            `json:"enabled" yaml:"enabled"`
	Port            string            `json:"port" yaml:"port"`
	Path            string            `json:"path" yaml:"path"`
	AuthEnabled     bool              `json:"auth_enabled" yaml:"auth_enabled"`
	BearerToken     string            `json:"bearer_token" yaml:"bearer_token"`
	CollectInterval string            `json:"collect_interval" yaml:"collect_interval"`
	RetentionPeriod string            `json:"retention_period" yaml:"retention_period"`
	Labels          map[string]string `json:"labels" yaml:"labels"`
	Prometheus      struct {
		Enabled   string `json:"enabled" yaml:"enabled"`
		Namespace string `json:"namespace" yaml:"namespace"`
		Subsystem string `json:"subsystem" yaml:"subsystem"`
		Path      string `json:"path" yaml:"path"`
	} `json:"prometheus" yaml:"prometheus"`
}

// LoggingConfig represents the logging configuration
type LoggingConfig struct {
	Enabled    string
	Level      string
	File       string
	MaxSize    string
	MaxBackups string
	MaxAge     string
	Compress   string
	Format     string
}

// Backend represents a backend server configuration
type Backend struct {
	URL            string
	Weight         string
	MaxConnections string
	Healthy        int
}

var db *sql.DB

// InitDB initializes the database
func InitDB(dbPath string) error {
	var err error

	// Check if database file exists
	_, err = os.Stat(dbPath)
	dbExists := err == nil

	// Open database
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %v", err)
	}

	// Test database connection
	if err := db.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %v", err)
	}

	// If database doesn't exist, create schema
	if !dbExists {
		// Create tables
		_, err = db.Exec(`
			CREATE TABLE IF NOT EXISTS server_config (
				id INTEGER PRIMARY KEY,
				port TEXT,
				worker_count TEXT,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);

			CREATE TABLE IF NOT EXISTS tls_config (
				id INTEGER PRIMARY KEY,
				enabled TEXT,
				cert_file TEXT,
				key_file TEXT,
				min_version TEXT,
				cipher_suites TEXT,
				client_auth TEXT,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);

			CREATE TABLE IF NOT EXISTS load_balancing_config (
				id INTEGER PRIMARY KEY,
				strategy TEXT,
				ip_hash_enabled TEXT,
				ip_hash_header TEXT,
				ip_hash_fallback_header TEXT,
				weighted_round_robin_enabled TEXT,
				weighted_round_robin_weight_by_capacity TEXT,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);

			CREATE TABLE IF NOT EXISTS connection_pool_config (
				id INTEGER PRIMARY KEY,
				enabled TEXT,
				max_idle_conns TEXT,
				max_idle_conns_per_host TEXT,
				max_conns_per_host TEXT,
				idle_conn_timeout TEXT,
				max_lifetime TEXT,
				keep_alive TEXT,
				dial_timeout TEXT,
				tls_handshake_timeout TEXT,
				expect_continue_timeout TEXT,
				response_header_timeout TEXT,
				disable_keep_alives INTEGER,
				disable_compression INTEGER,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);

			CREATE TABLE IF NOT EXISTS cache_config (
				id INTEGER PRIMARY KEY,
				enabled TEXT,
				ttl TEXT,
				max_size TEXT,
				headers TEXT,
				methods TEXT,
				status_codes TEXT,
				exclude_paths TEXT,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);

			CREATE TABLE IF NOT EXISTS circuit_breaker_config (
				id INTEGER PRIMARY KEY,
				enabled TEXT,
				failure_threshold TEXT,
				success_threshold TEXT,
				reset_timeout TEXT,
				half_open_timeout TEXT,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);

			CREATE TABLE IF NOT EXISTS metrics_config (
				id INTEGER PRIMARY KEY,
				enabled TEXT,
				port TEXT,
				path TEXT,
				auth_enabled INTEGER,
				bearer_token TEXT,
				collect_interval TEXT,
				retention_period TEXT,
				labels TEXT,
				prometheus_enabled TEXT,
				prometheus_namespace TEXT,
				prometheus_subsystem TEXT,
				prometheus_path TEXT,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);

			CREATE TABLE IF NOT EXISTS backends (
				id INTEGER PRIMARY KEY,
				url TEXT UNIQUE,
				weight TEXT,
				max_connections TEXT,
				healthy INTEGER,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);

			CREATE TABLE IF NOT EXISTS logging_config (
				id INTEGER PRIMARY KEY,
				enabled TEXT,
				level TEXT,
				file TEXT,
				max_size TEXT,
				max_backups TEXT,
				max_age TEXT,
				compress TEXT,
				format TEXT,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);

			-- Insert default values
			INSERT INTO server_config (id, port, worker_count) VALUES (1, '8080', '4');
			INSERT INTO tls_config (id, enabled, cert_file, key_file, min_version, cipher_suites, client_auth) 
				VALUES (1, 'false', '', '', 'TLS1.2', '[]', 'none');
			INSERT INTO load_balancing_config (id, strategy, ip_hash_enabled, ip_hash_header, ip_hash_fallback_header, 
				weighted_round_robin_enabled, weighted_round_robin_weight_by_capacity) 
				VALUES (1, 'round_robin', 'false', 'X-Forwarded-For', 'X-Real-IP', 'false', 'false');
			INSERT INTO connection_pool_config (id, enabled, max_idle_conns, max_idle_conns_per_host, max_conns_per_host, 
				idle_conn_timeout, max_lifetime, keep_alive, dial_timeout, tls_handshake_timeout, 
				expect_continue_timeout, response_header_timeout, disable_keep_alives, disable_compression) 
				VALUES (1, 'true', '100', '10', '100', '90s', '0s', '30s', '30s', '10s', '1s', '0s', 0, 0);
			INSERT INTO cache_config (id, enabled, ttl, max_size, headers, methods, status_codes, exclude_paths) 
				VALUES (1, 'false', '5m', '100MB', '[]', '[]', '[]', '[]');
			INSERT INTO circuit_breaker_config (id, enabled, failure_threshold, success_threshold, reset_timeout, half_open_timeout) 
				VALUES (1, 'false', '5', '2', '30s', '5s');
			INSERT INTO metrics_config (id, enabled, port, path, auth_enabled, bearer_token, collect_interval, 
				retention_period, labels, prometheus_enabled, prometheus_namespace, prometheus_subsystem, prometheus_path) 
				VALUES (1, 'false', '9090', '/metrics', 0, '', '15s', '24h', '{}', 'false', 'loadbalancer', 'server', '/metrics');
			INSERT INTO logging_config (id, enabled, level, file, max_size, max_backups, max_age, compress, format) 
				VALUES (1, 'true', 'info', 'loadbalancer.log', '100MB', '3', '7d', 'true', 'json');
		`)
		if err != nil {
			return fmt.Errorf("failed to create tables: %v", err)
		}
	}

	return nil
}

// CloseDB closes the database connection
func CloseDB() {
	if db != nil {
		db.Close()
	}
}

// GetTLSConfig retrieves TLS configuration from database
func GetTLSConfig() (map[string]interface{}, error) {
	var config map[string]interface{}
	row := db.QueryRow("SELECT * FROM tls_config WHERE id = 1")

	var id int
	var enabled, certFile, keyFile, minVersion, cipherSuites, clientAuth string
	var updatedAt time.Time

	err := row.Scan(&id, &enabled, &certFile, &keyFile, &minVersion, &cipherSuites, &clientAuth, &updatedAt)
	if err != nil {
		return nil, err
	}

	config = map[string]interface{}{
		"enabled":       enabled,
		"cert_file":     certFile,
		"key_file":      keyFile,
		"min_version":   minVersion,
		"cipher_suites": cipherSuites,
		"client_auth":   clientAuth,
	}

	return config, nil
}

// UpdateTLSConfig updates the TLS configuration
func UpdateTLSConfig(config TLSConfig) error {
	// Convert CipherSuites to JSON string
	cipherSuitesJSON, err := json.Marshal(config.CipherSuites)
	if err != nil {
		return fmt.Errorf("failed to marshal cipher suites: %v", err)
	}

	_, err = db.Exec(`
		UPDATE tls_config 
		SET enabled = ?, cert_file = ?, key_file = ?, min_version = ?, cipher_suites = ?, client_auth = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = 1`,
		config.Enabled, config.CertFile, config.KeyFile, config.MinVersion, string(cipherSuitesJSON), config.ClientAuth)
	return err
}

// GetLoadBalancingConfig retrieves load balancing configuration
func GetLoadBalancingConfig() (map[string]interface{}, error) {
	var config map[string]interface{}
	row := db.QueryRow("SELECT * FROM load_balancing_config WHERE id = 1")

	var id int
	var strategy, ipHashEnabled, ipHashHeader, ipHashFallbackHeader,
		weightedRoundRobinEnabled, weightedRoundRobinWeightByCapacity string
	var updatedAt time.Time

	err := row.Scan(&id, &strategy, &ipHashEnabled, &ipHashHeader, &ipHashFallbackHeader,
		&weightedRoundRobinEnabled, &weightedRoundRobinWeightByCapacity, &updatedAt)
	if err != nil {
		return nil, err
	}

	config = map[string]interface{}{
		"strategy":                                strategy,
		"ip_hash_enabled":                         ipHashEnabled,
		"ip_hash_header":                          ipHashHeader,
		"ip_hash_fallback_header":                 ipHashFallbackHeader,
		"weighted_round_robin_enabled":            weightedRoundRobinEnabled,
		"weighted_round_robin_weight_by_capacity": weightedRoundRobinWeightByCapacity,
	}

	return config, nil
}

// UpdateLoadBalancingConfig updates the load balancing configuration
func UpdateLoadBalancingConfig(config LoadBalancingConfig) error {
	_, err := db.Exec(`
		UPDATE load_balancing_config 
		SET strategy = ?, ip_hash_enabled = ?, ip_hash_header = ?, ip_hash_fallback_header = ?, 
			weighted_round_robin_enabled = ?, weighted_round_robin_weight_by_capacity = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = 1`,
		config.Strategy, config.IPHash.Enabled, config.IPHash.Header, config.IPHash.FallbackHeader,
		config.WeightedRoundRobin.Enabled, config.WeightedRoundRobin.WeightByCapacity)
	return err
}

// GetBackends retrieves all backends
func GetBackends() ([]map[string]interface{}, error) {
	rows, err := db.Query("SELECT * FROM backends")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var backends []map[string]interface{}
	for rows.Next() {
		var id int
		var url, weight, maxConnections string
		var healthy int
		var updatedAt time.Time

		err := rows.Scan(&id, &url, &weight, &maxConnections, &healthy, &updatedAt)
		if err != nil {
			return nil, err
		}

		backend := map[string]interface{}{
			"url":             url,
			"weight":          weight,
			"max_connections": maxConnections,
			"healthy":         healthy == 1,
		}
		backends = append(backends, backend)
	}

	return backends, nil
}

// UpdateBackends updates the backends configuration
func UpdateBackends(backends []Backend) error {
	// First, delete all existing backends
	_, err := db.Exec("DELETE FROM backends")
	if err != nil {
		return err
	}

	// Then insert the new backends
	for _, b := range backends {
		_, err := db.Exec(`
			INSERT INTO backends (url, weight, max_connections, healthy, updated_at)
			VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`,
			b.URL, b.Weight, b.MaxConnections, b.Healthy)
		if err != nil {
			return err
		}
	}
	return nil
}

// GetMetricsConfig retrieves metrics configuration
func GetMetricsConfig() (map[string]interface{}, error) {
	var config map[string]interface{}
	row := db.QueryRow("SELECT * FROM metrics_config WHERE id = 1")

	var id int
	var enabled, port, path, bearerToken, collectInterval, retentionPeriod, labels,
		prometheusEnabled, prometheusNamespace, prometheusSubsystem, prometheusPath string
	var authEnabled int
	var updatedAt time.Time

	err := row.Scan(&id, &enabled, &port, &path, &authEnabled, &bearerToken,
		&collectInterval, &retentionPeriod, &labels, &prometheusEnabled,
		&prometheusNamespace, &prometheusSubsystem, &prometheusPath, &updatedAt)
	if err != nil {
		return nil, err
	}

	config = map[string]interface{}{
		"enabled":              enabled,
		"port":                 port,
		"path":                 path,
		"auth_enabled":         authEnabled == 1,
		"bearer_token":         bearerToken,
		"collect_interval":     collectInterval,
		"retention_period":     retentionPeriod,
		"labels":               labels,
		"prometheus_enabled":   prometheusEnabled,
		"prometheus_namespace": prometheusNamespace,
		"prometheus_subsystem": prometheusSubsystem,
		"prometheus_path":      prometheusPath,
	}

	return config, nil
}

// UpdateMetricsConfig updates the metrics configuration
func UpdateMetricsConfig(config MetricsConfig) error {
	// Convert Labels map to JSON string
	labelsJSON, err := json.Marshal(config.Labels)
	if err != nil {
		return fmt.Errorf("failed to marshal labels: %v", err)
	}

	// Convert auth_enabled to int
	authEnabled := 0
	if config.AuthEnabled {
		authEnabled = 1
	}

	_, err = db.Exec(`
		UPDATE metrics_config 
		SET enabled = ?, port = ?, path = ?, auth_enabled = ?, bearer_token = ?,
			collect_interval = ?, retention_period = ?, labels = ?, prometheus_enabled = ?,
			prometheus_namespace = ?, prometheus_subsystem = ?, prometheus_path = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = 1`,
		config.Enabled, config.Port, config.Path, authEnabled, config.BearerToken,
		config.CollectInterval, config.RetentionPeriod, string(labelsJSON), config.Prometheus.Enabled,
		config.Prometheus.Namespace, config.Prometheus.Subsystem, config.Prometheus.Path)
	return err
}

// GetLoggingConfig retrieves logging configuration
func GetLoggingConfig() (map[string]interface{}, error) {
	var config map[string]interface{}
	row := db.QueryRow("SELECT * FROM logging_config WHERE id = 1")

	var id int
	var enabled, level, file, maxSize, maxBackups, maxAge, compress, format string
	var updatedAt time.Time

	err := row.Scan(&id, &enabled, &level, &file, &maxSize, &maxBackups,
		&maxAge, &compress, &format, &updatedAt)
	if err != nil {
		return nil, err
	}

	config = map[string]interface{}{
		"enabled":     enabled,
		"level":       level,
		"file":        file,
		"max_size":    maxSize,
		"max_backups": maxBackups,
		"max_age":     maxAge,
		"compress":    compress,
		"format":      format,
	}

	return config, nil
}

// UpdateLoggingConfig updates the logging configuration
func UpdateLoggingConfig(config LoggingConfig) error {
	_, err := db.Exec(`
		UPDATE logging_config 
		SET enabled = ?, level = ?, file = ?, max_size = ?, max_backups = ?,
			max_age = ?, compress = ?, format = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = 1`,
		config.Enabled, config.Level, config.File, config.MaxSize, config.MaxBackups,
		config.MaxAge, config.Compress, config.Format)
	return err
}

// GetFullConfig retrieves the complete configuration
func GetFullConfig() (*Config, error) {
	log.Println("Getting full configuration from database")

	var serverConfig ServerConfig
	err := db.QueryRow("SELECT port, worker_count FROM server_config WHERE id = 1").Scan(
		&serverConfig.Port,
		&serverConfig.WorkerCount,
	)
	if err != nil {
		log.Printf("Error getting server config: %v", err)
		return nil, fmt.Errorf("failed to get server config: %v", err)
	}

	config := &Config{
		Server: serverConfig,
		TLS: TLSConfig{
			Enabled: "false",
		},
		LoadBalancing: LoadBalancingConfig{
			Strategy: "round_robin",
		},
		ConnectionPool: ConnectionPoolConfig{
			Enabled: "true",
		},
		Cache: CacheConfig{
			Enabled: "false",
		},
		CircuitBreaker: CircuitBreakerConfig{
			Enabled: "false",
		},
		Metrics: MetricsConfig{
			Enabled: "false",
		},
		Logging: LoggingConfig{
			Enabled: "false",
		},
	}

	// Get TLS config
	var tlsConfig TLSConfig
	var cipherSuitesStr string
	err = db.QueryRow("SELECT enabled, cert_file, key_file, min_version, cipher_suites, client_auth FROM tls_config WHERE id = 1").Scan(
		&tlsConfig.Enabled,
		&tlsConfig.CertFile,
		&tlsConfig.KeyFile,
		&tlsConfig.MinVersion,
		&cipherSuitesStr,
		&tlsConfig.ClientAuth,
	)
	if err != nil {
		log.Printf("Error getting TLS config: %v", err)
		return nil, fmt.Errorf("failed to get TLS config: %v", err)
	}
	if cipherSuitesStr != "" {
		if err := json.Unmarshal([]byte(cipherSuitesStr), &tlsConfig.CipherSuites); err != nil {
			log.Printf("Error unmarshaling cipher suites: %v", err)
			return nil, fmt.Errorf("failed to unmarshal cipher suites: %v", err)
		}
	} else {
		tlsConfig.CipherSuites = []string{} // Ensure it's an empty slice, not nil
	}
	config.TLS = tlsConfig

	// Get load balancing config
	var loadBalancingConfig LoadBalancingConfig
	err = db.QueryRow("SELECT strategy FROM load_balancing_config WHERE id = 1").Scan(
		&loadBalancingConfig.Strategy,
	)
	if err != nil {
		log.Printf("Error getting load balancing config: %v", err)
		return nil, fmt.Errorf("failed to get load balancing config: %v", err)
	}
	// Get IPHash config
	var ipHashConfig IPHashConfig
	err = db.QueryRow("SELECT ip_hash_enabled, ip_hash_header, ip_hash_fallback_header FROM load_balancing_config WHERE id = 1").Scan(
		&ipHashConfig.Enabled,
		&ipHashConfig.Header,
		&ipHashConfig.FallbackHeader,
	)
	if err != nil {
		log.Printf("Error getting IPHash config: %v", err)
		return nil, fmt.Errorf("failed to get IPHash config: %v", err)
	}
	loadBalancingConfig.IPHash = ipHashConfig

	// Get weighted round robin config
	var weightedRoundRobinConfig WeightedRoundRobinConfig
	err = db.QueryRow("SELECT weighted_round_robin_enabled, weighted_round_robin_weight_by_capacity FROM load_balancing_config WHERE id = 1").Scan(
		&weightedRoundRobinConfig.Enabled,
		&weightedRoundRobinConfig.WeightByCapacity,
	)
	if err != nil {
		log.Printf("Error getting WeightedRoundRobin config: %v", err)
		return nil, fmt.Errorf("failed to get WeightedRoundRobin config: %v", err)
	}
	loadBalancingConfig.WeightedRoundRobin = weightedRoundRobinConfig

	config.LoadBalancing = loadBalancingConfig

	// Get connection pool config
	var cp ConnectionPoolConfig
	err = db.QueryRow("SELECT enabled, max_idle_conns, max_idle_conns_per_host, max_conns_per_host, idle_conn_timeout, max_lifetime, keep_alive, dial_timeout, tls_handshake_timeout, expect_continue_timeout, response_header_timeout, disable_keep_alives, disable_compression FROM connection_pool_config WHERE id = 1").Scan(
		&cp.Enabled,
		&cp.MaxIdleConns,
		&cp.MaxIdleConnsPerHost,
		&cp.MaxConnsPerHost,
		&cp.IdleConnTimeout,
		&cp.MaxLifetime,
		&cp.KeepAlive,
		&cp.DialTimeout,
		&cp.TLSHandshakeTimeout,
		&cp.ExpectContinueTimeout,
		&cp.ResponseHeaderTimeout,
		&cp.DisableKeepAlives,
		&cp.DisableCompression,
	)
	if err != nil {
		log.Printf("Error getting connection pool config: %v", err)
		return nil, fmt.Errorf("failed to get connection pool config: %v", err)
	}
	config.ConnectionPool = cp

	// Get cache config
	var cache CacheConfig
	var headersStr string
	var methodsStr string
	var statusCodesStr string
	var excludePathsStr string
	err = db.QueryRow("SELECT enabled, ttl, max_size, headers, methods, status_codes, exclude_paths FROM cache_config WHERE id = 1").Scan(
		&cache.Enabled,
		&cache.TTL,
		&cache.MaxSize,
		&headersStr,
		&methodsStr,
		&statusCodesStr,
		&excludePathsStr,
	)
	if err != nil {
		log.Printf("Error getting cache config: %v", err)
		return nil, fmt.Errorf("failed to get cache config: %v", err)
	}
	if headersStr != "" {
		if err := json.Unmarshal([]byte(headersStr), &cache.Headers); err != nil {
			log.Printf("Error unmarshaling cache headers: %v", err)
			return nil, fmt.Errorf("failed to unmarshal cache headers: %v", err)
		}
	}
	if methodsStr != "" {
		if err := json.Unmarshal([]byte(methodsStr), &cache.Methods); err != nil {
			log.Printf("Error unmarshaling cache methods: %v", err)
			return nil, fmt.Errorf("failed to unmarshal cache methods: %v", err)
		}
	}
	if statusCodesStr != "" {
		if err := json.Unmarshal([]byte(statusCodesStr), &cache.StatusCodes); err != nil {
			log.Printf("Error unmarshaling cache status codes: %v", err)
			return nil, fmt.Errorf("failed to unmarshal cache status codes: %v", err)
		}
	}
	if excludePathsStr != "" {
		if err := json.Unmarshal([]byte(excludePathsStr), &cache.ExcludePaths); err != nil {
			log.Printf("Error unmarshaling cache exclude paths: %v", err)
			return nil, fmt.Errorf("failed to unmarshal cache exclude paths: %v", err)
		}
	}
	config.Cache = cache

	// Get circuit breaker config
	var cb CircuitBreakerConfig
	err = db.QueryRow("SELECT enabled, failure_threshold, success_threshold, reset_timeout, half_open_timeout FROM circuit_breaker_config WHERE id = 1").Scan(
		&cb.Enabled,
		&cb.FailureThreshold,
		&cb.SuccessThreshold,
		&cb.ResetTimeout,
		&cb.HalfOpenTimeout,
	)
	if err != nil {
		log.Printf("Error getting circuit breaker config: %v", err)
		return nil, fmt.Errorf("failed to get circuit breaker config: %v", err)
	}
	config.CircuitBreaker = cb

	// Get metrics config
	var metricsConfig MetricsConfig
	var labelsStr string
	err = db.QueryRow(`
		SELECT enabled, port, path, auth_enabled, bearer_token, 
		       collect_interval, retention_period, labels, 
		       prometheus_enabled, prometheus_namespace, prometheus_subsystem, prometheus_path 
		FROM metrics_config WHERE id = 1`).Scan(
		&metricsConfig.Enabled,
		&metricsConfig.Port,
		&metricsConfig.Path,
		&metricsConfig.AuthEnabled,
		&metricsConfig.BearerToken,
		&metricsConfig.CollectInterval,
		&metricsConfig.RetentionPeriod,
		&labelsStr,
		&metricsConfig.Prometheus.Enabled,
		&metricsConfig.Prometheus.Namespace,
		&metricsConfig.Prometheus.Subsystem,
		&metricsConfig.Prometheus.Path,
	)
	if err != nil {
		log.Printf("Error getting metrics config: %v", err)
		return nil, fmt.Errorf("failed to get metrics config: %v", err)
	}

	// Debug logging
	log.Printf("Raw metrics config from database: %+v", metricsConfig)

	// Initialize labels map if empty
	if labelsStr == "" {
		metricsConfig.Labels = make(map[string]string)
	} else {
		if err := json.Unmarshal([]byte(labelsStr), &metricsConfig.Labels); err != nil {
			log.Printf("Error unmarshaling labels: %v", err)
			return nil, fmt.Errorf("failed to unmarshal labels: %v", err)
		}
	}

	// Set default values only if fields are empty
	if metricsConfig.Port == "" {
		metricsConfig.Port = "9090"
	}
	if metricsConfig.Path == "" {
		metricsConfig.Path = "/metrics"
	}
	if metricsConfig.CollectInterval == "" {
		metricsConfig.CollectInterval = "15s"
	}
	if metricsConfig.RetentionPeriod == "" {
		metricsConfig.RetentionPeriod = "24h"
	}
	if metricsConfig.Prometheus.Enabled == "" {
		metricsConfig.Prometheus.Enabled = "false"
	}
	if metricsConfig.Prometheus.Namespace == "" {
		metricsConfig.Prometheus.Namespace = "loadbalancer"
	}
	if metricsConfig.Prometheus.Subsystem == "" {
		metricsConfig.Prometheus.Subsystem = "server"
	}
	if metricsConfig.Prometheus.Path == "" {
		metricsConfig.Prometheus.Path = "/metrics"
	}

	// Debug logging
	log.Printf("Final metrics config: %+v", metricsConfig)

	config.Metrics = metricsConfig

	// Get logging config
	var logging LoggingConfig
	err = db.QueryRow("SELECT enabled, level, file, max_size, max_backups, max_age, compress, format FROM logging_config WHERE id = 1").Scan(
		&logging.Enabled,
		&logging.Level,
		&logging.File,
		&logging.MaxSize,
		&logging.MaxBackups,
		&logging.MaxAge,
		&logging.Compress,
		&logging.Format,
	)
	if err != nil {
		log.Printf("Error getting logging config: %v", err)
		return nil, fmt.Errorf("failed to get logging config: %v", err)
	}
	config.Logging = logging

	// Get backends
	rows, err := db.Query("SELECT url, weight, max_connections, healthy FROM backends")
	if err != nil {
		log.Printf("Error getting backends: %v", err)
		return nil, fmt.Errorf("failed to get backends: %v", err)
	}
	defer rows.Close()

	var backends []Backend
	for rows.Next() {
		var b Backend
		err := rows.Scan(&b.URL, &b.Weight, &b.MaxConnections, &b.Healthy)
		if err != nil {
			log.Printf("Error scanning backend: %v", err)
			return nil, fmt.Errorf("failed to scan backend: %v", err)
		}
		backends = append(backends, b)
	}
	if err = rows.Err(); err != nil {
		log.Printf("Error iterating backends: %v", err)
		return nil, fmt.Errorf("failed to iterate backends: %v", err)
	}
	config.Backends = backends

	log.Println("Successfully retrieved full configuration from database")
	return config, nil
}

// UpdateFullConfig updates the complete configuration
func UpdateFullConfig(config *Config) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// Update TLS config
	if err := UpdateTLSConfig(config.TLS); err != nil {
		return fmt.Errorf("failed to update TLS config: %v", err)
	}

	// Update load balancing config
	if err := UpdateLoadBalancingConfig(config.LoadBalancing); err != nil {
		return fmt.Errorf("failed to update load balancing config: %v", err)
	}

	// Update connection pool config
	if err := UpdateConnectionPoolConfig(config.ConnectionPool); err != nil {
		return fmt.Errorf("failed to update connection pool config: %v", err)
	}

	// Update cache config
	if err := UpdateCacheConfig(config.Cache); err != nil {
		return fmt.Errorf("failed to update cache config: %v", err)
	}

	// Update circuit breaker config
	if err := UpdateCircuitBreakerConfig(config.CircuitBreaker); err != nil {
		return fmt.Errorf("failed to update circuit breaker config: %v", err)
	}

	// Update metrics config
	if err := UpdateMetricsConfig(config.Metrics); err != nil {
		return fmt.Errorf("failed to update metrics config: %v", err)
	}

	// Update logging config
	if err := UpdateLoggingConfig(config.Logging); err != nil {
		return fmt.Errorf("failed to update logging config: %v", err)
	}

	// Update backends
	if err := UpdateBackends(config.Backends); err != nil {
		return fmt.Errorf("failed to update backends: %v", err)
	}

	return tx.Commit()
}

// UpdateConnectionPoolConfig updates the connection pool configuration
func UpdateConnectionPoolConfig(config ConnectionPoolConfig) error {
	_, err := db.Exec(`
		UPDATE connection_pool_config 
		SET enabled = ?, max_idle_conns = ?, max_idle_conns_per_host = ?, max_conns_per_host = ?,
			idle_conn_timeout = ?, max_lifetime = ?, keep_alive = ?, dial_timeout = ?,
			tls_handshake_timeout = ?, expect_continue_timeout = ?, response_header_timeout = ?,
			disable_keep_alives = ?, disable_compression = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = 1`,
		config.Enabled, config.MaxIdleConns, config.MaxIdleConnsPerHost, config.MaxConnsPerHost,
		config.IdleConnTimeout, config.MaxLifetime, config.KeepAlive, config.DialTimeout,
		config.TLSHandshakeTimeout, config.ExpectContinueTimeout, config.ResponseHeaderTimeout,
		config.DisableKeepAlives, config.DisableCompression)
	return err
}

// UpdateCacheConfig updates the cache configuration
func UpdateCacheConfig(config CacheConfig) error {
	// Convert arrays to JSON strings
	headersJSON, err := json.Marshal(config.Headers)
	if err != nil {
		return fmt.Errorf("failed to marshal headers: %v", err)
	}

	methodsJSON, err := json.Marshal(config.Methods)
	if err != nil {
		return fmt.Errorf("failed to marshal methods: %v", err)
	}

	statusCodesJSON, err := json.Marshal(config.StatusCodes)
	if err != nil {
		return fmt.Errorf("failed to marshal status codes: %v", err)
	}

	excludePathsJSON, err := json.Marshal(config.ExcludePaths)
	if err != nil {
		return fmt.Errorf("failed to marshal exclude paths: %v", err)
	}

	_, err = db.Exec(`
		UPDATE cache_config 
		SET enabled = ?, ttl = ?, max_size = ?, headers = ?, methods = ?, status_codes = ?, exclude_paths = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = 1`,
		config.Enabled, config.TTL, config.MaxSize, string(headersJSON), string(methodsJSON), string(statusCodesJSON), string(excludePathsJSON))
	return err
}

// UpdateCircuitBreakerConfig updates the circuit breaker configuration
func UpdateCircuitBreakerConfig(config CircuitBreakerConfig) error {
	_, err := db.Exec(`
		UPDATE circuit_breaker_config 
		SET enabled = ?, failure_threshold = ?, success_threshold = ?, reset_timeout = ?, half_open_timeout = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = 1`,
		config.Enabled, config.FailureThreshold, config.SuccessThreshold, config.ResetTimeout, config.HalfOpenTimeout)
	return err
}

// MigrateNulls updates all NULL string fields in config tables to empty strings
func MigrateNulls(db *sql.DB) error {
	stmts := []string{
		`UPDATE tls_config SET cert_file = COALESCE(cert_file, ''), key_file = COALESCE(key_file, ''), min_version = COALESCE(min_version, ''), cipher_suites = COALESCE(cipher_suites, ''), client_auth = COALESCE(client_auth, '') WHERE id = 1;`,
		`UPDATE load_balancing_config SET ip_hash_enabled = COALESCE(ip_hash_enabled, ''), ip_hash_header = COALESCE(ip_hash_header, ''), ip_hash_fallback_header = COALESCE(ip_hash_fallback_header, ''), weighted_round_robin_enabled = COALESCE(weighted_round_robin_enabled, ''), weighted_round_robin_weight_by_capacity = COALESCE(weighted_round_robin_weight_by_capacity, '') WHERE id = 1;`,
		`UPDATE connection_pool_config SET max_idle_conns = COALESCE(max_idle_conns, ''), max_idle_conns_per_host = COALESCE(max_idle_conns_per_host, ''), max_conns_per_host = COALESCE(max_conns_per_host, ''), idle_conn_timeout = COALESCE(idle_conn_timeout, ''), max_lifetime = COALESCE(max_lifetime, ''), keep_alive = COALESCE(keep_alive, ''), dial_timeout = COALESCE(dial_timeout, ''), tls_handshake_timeout = COALESCE(tls_handshake_timeout, ''), expect_continue_timeout = COALESCE(expect_continue_timeout, ''), response_header_timeout = COALESCE(response_header_timeout, ''), disable_keep_alives = COALESCE(disable_keep_alives, 0), disable_compression = COALESCE(disable_compression, 0) WHERE id = 1;`,
		`UPDATE cache_config SET ttl = COALESCE(ttl, ''), max_size = COALESCE(max_size, ''), headers = COALESCE(headers, ''), methods = COALESCE(methods, ''), status_codes = COALESCE(status_codes, ''), exclude_paths = COALESCE(exclude_paths, ''), updated_at = CURRENT_TIMESTAMP WHERE id = 1;`,
		`UPDATE circuit_breaker_config SET failure_threshold = COALESCE(failure_threshold, ''), success_threshold = COALESCE(success_threshold, ''), reset_timeout = COALESCE(reset_timeout, ''), half_open_timeout = COALESCE(half_open_timeout, '') WHERE id = 1;`,
		`UPDATE metrics_config SET port = COALESCE(port, ''), path = COALESCE(path, ''), bearer_token = COALESCE(bearer_token, ''), collect_interval = COALESCE(collect_interval, ''), retention_period = COALESCE(retention_period, ''), labels = COALESCE(labels, ''), prometheus_enabled = COALESCE(prometheus_enabled, ''), prometheus_namespace = COALESCE(prometheus_namespace, ''), prometheus_subsystem = COALESCE(prometheus_subsystem, ''), prometheus_path = COALESCE(prometheus_path, ''), auth_enabled = COALESCE(auth_enabled, 0) WHERE id = 1;`,
		`UPDATE logging_config SET level = COALESCE(level, ''), file = COALESCE(file, ''), max_size = COALESCE(max_size, ''), max_backups = COALESCE(max_backups, ''), max_age = COALESCE(max_age, ''), compress = COALESCE(compress, ''), format = COALESCE(format, '') WHERE id = 1;`,
		`UPDATE backends SET url = COALESCE(url, ''), weight = COALESCE(weight, ''), max_connections = COALESCE(max_connections, '') WHERE url IS NULL OR weight IS NULL OR max_connections IS NULL;`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// InitializeDefaultConfig initializes the database with default values from config.yml
func InitializeDefaultConfig() error {
	// Read config.yml
	configData, err := os.ReadFile("config.yml")
	if err != nil {
		return fmt.Errorf("failed to read config.yml: %v", err)
	}

	// Parse YAML into Config struct
	var config Config
	if err := yaml.Unmarshal(configData, &config); err != nil {
		return fmt.Errorf("failed to parse config.yml: %v", err)
	}

	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %v", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// Update server config
	_, err = tx.Exec(`
		INSERT OR REPLACE INTO server_config (id, port, worker_count)
		VALUES (1, ?, ?)`,
		config.Server.Port, config.Server.WorkerCount)
	if err != nil {
		return fmt.Errorf("failed to update server config: %v", err)
	}

	// Update TLS config
	cipherSuitesJSON, err := json.Marshal(config.Server.TLS.CipherSuites)
	if err != nil {
		return fmt.Errorf("failed to marshal cipher suites: %v", err)
	}
	_, err = tx.Exec(`
		INSERT OR REPLACE INTO tls_config (id, enabled, cert_file, key_file, min_version, cipher_suites, client_auth)
		VALUES (1, ?, ?, ?, ?, ?, ?)`,
		config.Server.TLS.Enabled, config.Server.TLS.CertFile, config.Server.TLS.KeyFile,
		config.Server.TLS.MinVersion, string(cipherSuitesJSON), config.Server.TLS.ClientAuth)
	if err != nil {
		return fmt.Errorf("failed to update TLS config: %v", err)
	}

	// Update load balancing config
	_, err = tx.Exec(`
		INSERT OR REPLACE INTO load_balancing_config (id, strategy, ip_hash_enabled, ip_hash_header, ip_hash_fallback_header,
			weighted_round_robin_enabled, weighted_round_robin_weight_by_capacity)
		VALUES (1, ?, ?, ?, ?, ?, ?)`,
		config.Server.LoadBalancing.Strategy,
		config.Server.LoadBalancing.IPHash.Enabled,
		config.Server.LoadBalancing.IPHash.Header,
		config.Server.LoadBalancing.IPHash.FallbackHeader,
		config.Server.LoadBalancing.WeightedRoundRobin.Enabled,
		config.Server.LoadBalancing.WeightedRoundRobin.WeightByCapacity)
	if err != nil {
		return fmt.Errorf("failed to update load balancing config: %v", err)
	}

	// Update connection pool config
	_, err = tx.Exec(`
		INSERT OR REPLACE INTO connection_pool_config (id, enabled, max_idle_conns, max_idle_conns_per_host,
			max_conns_per_host, idle_conn_timeout, max_lifetime, keep_alive, dial_timeout,
			tls_handshake_timeout, expect_continue_timeout, response_header_timeout,
			disable_keep_alives, disable_compression)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		config.Server.ConnectionPool.Enabled,
		config.Server.ConnectionPool.MaxIdleConns,
		config.Server.ConnectionPool.MaxIdleConnsPerHost,
		config.Server.ConnectionPool.MaxConnsPerHost,
		config.Server.ConnectionPool.IdleConnTimeout,
		config.Server.ConnectionPool.MaxLifetime,
		config.Server.ConnectionPool.KeepAlive,
		config.Server.ConnectionPool.DialTimeout,
		config.Server.ConnectionPool.TLSHandshakeTimeout,
		config.Server.ConnectionPool.ExpectContinueTimeout,
		config.Server.ConnectionPool.ResponseHeaderTimeout,
		config.Server.ConnectionPool.DisableKeepAlives,
		config.Server.ConnectionPool.DisableCompression)
	if err != nil {
		return fmt.Errorf("failed to update connection pool config: %v", err)
	}

	// Update cache config
	headersJSON, err := json.Marshal(config.Server.Cache.Headers)
	if err != nil {
		return fmt.Errorf("failed to marshal cache headers: %v", err)
	}
	methodsJSON, err := json.Marshal(config.Server.Cache.Methods)
	if err != nil {
		return fmt.Errorf("failed to marshal cache methods: %v", err)
	}
	statusCodesJSON, err := json.Marshal(config.Server.Cache.StatusCodes)
	if err != nil {
		return fmt.Errorf("failed to marshal cache status codes: %v", err)
	}
	excludePathsJSON, err := json.Marshal(config.Server.Cache.ExcludePaths)
	if err != nil {
		return fmt.Errorf("failed to marshal cache exclude paths: %v", err)
	}
	_, err = tx.Exec(`
		INSERT OR REPLACE INTO cache_config (id, enabled, ttl, max_size, headers, methods, status_codes, exclude_paths)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?)`,
		config.Server.Cache.Enabled,
		config.Server.Cache.TTL,
		config.Server.Cache.MaxSize,
		string(headersJSON),
		string(methodsJSON),
		string(statusCodesJSON),
		string(excludePathsJSON))
	if err != nil {
		return fmt.Errorf("failed to update cache config: %v", err)
	}

	// Update circuit breaker config
	_, err = tx.Exec(`
		INSERT OR REPLACE INTO circuit_breaker_config (id, enabled, failure_threshold, success_threshold,
			reset_timeout, half_open_timeout)
		VALUES (1, ?, ?, ?, ?, ?)`,
		config.Server.CircuitBreaker.Enabled,
		config.Server.CircuitBreaker.FailureThreshold,
		config.Server.CircuitBreaker.SuccessThreshold,
		config.Server.CircuitBreaker.ResetTimeout,
		config.Server.CircuitBreaker.HalfOpenTimeout)
	if err != nil {
		return fmt.Errorf("failed to update circuit breaker config: %v", err)
	}

	// Update metrics config
	labelsJSON, err := json.Marshal(config.Metrics.Labels)
	if err != nil {
		return fmt.Errorf("failed to marshal metrics labels: %v", err)
	}
	_, err = tx.Exec(`
		INSERT OR REPLACE INTO metrics_config (id, enabled, port, path, auth_enabled, bearer_token,
			collect_interval, retention_period, labels, prometheus_enabled,
			prometheus_namespace, prometheus_subsystem, prometheus_path)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		config.Metrics.Enabled,
		config.Metrics.Port,
		config.Metrics.Path,
		config.Metrics.AuthEnabled,
		config.Metrics.BearerToken,
		config.Metrics.CollectInterval,
		config.Metrics.RetentionPeriod,
		string(labelsJSON),
		config.Metrics.Prometheus.Enabled,
		config.Metrics.Prometheus.Namespace,
		config.Metrics.Prometheus.Subsystem,
		config.Metrics.Prometheus.Path)
	if err != nil {
		return fmt.Errorf("failed to update metrics config: %v", err)
	}

	// Update logging config
	_, err = tx.Exec(`
		INSERT OR REPLACE INTO logging_config (id, enabled, level, file, max_size, max_backups,
			max_age, compress, format)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?)`,
		config.Logging.Enabled,
		config.Logging.Level,
		config.Logging.File,
		config.Logging.MaxSize,
		config.Logging.MaxBackups,
		config.Logging.MaxAge,
		config.Logging.Compress,
		config.Logging.Format)
	if err != nil {
		return fmt.Errorf("failed to update logging config: %v", err)
	}

	// Update backends
	_, err = tx.Exec("DELETE FROM backends")
	if err != nil {
		return fmt.Errorf("failed to clear backends: %v", err)
	}
	for _, b := range config.Backends {
		_, err = tx.Exec(`
			INSERT INTO backends (url, weight, max_connections, healthy)
			VALUES (?, ?, ?, ?)`,
			b.URL, b.Weight, b.MaxConnections, b.Healthy)
		if err != nil {
			return fmt.Errorf("failed to insert backend: %v", err)
		}
	}

	// Commit the transaction
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %v", err)
	}

	return nil
}
