package gui

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"loadbalancer/internal/database"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// Config represents the dashboard configuration
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
	Dashboard struct {
		Port int `yaml:"port"`
		Auth struct {
			Enabled        string `yaml:"enabled"`
			Username       string `yaml:"username"`
			Password       string `yaml:"password"`
			SessionTimeout string `yaml:"session_timeout"`
		} `yaml:"auth"`
	} `yaml:"dashboard"`
}

//go:embed templates
var templates embed.FS

// Dashboard represents the dashboard instance
type Dashboard struct {
	config      *Config
	sessions    map[string]time.Time
	sessionMu   sync.RWMutex
	template    *template.Template
	configPath  string
	configMutex sync.RWMutex
}

// LoadConfig loads the initial configuration from a YAML file
func LoadConfig(path string) (*Config, error) {
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

// NewDashboard creates a new dashboard instance
func NewDashboard(config *Config) *Dashboard {
	// Load HTML template
	tmpl, err := template.ParseFiles("gui/templates/index.html")
	if err != nil {
		log.Fatalf("Failed to load template: %v", err)
	}

	return &Dashboard{
		config:     config,
		configPath: "config.yml",
		sessions:   make(map[string]time.Time),
		template:   tmpl,
	}
}

// saveConfig saves the configuration to a YAML file
func (d *Dashboard) saveConfig(config *Config) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("error marshaling config: %v", err)
	}

	if err := os.WriteFile(d.configPath, data, 0644); err != nil {
		return fmt.Errorf("error writing config file: %v", err)
	}

	return nil
}

// isAuthenticated checks if the user is authenticated
func (d *Dashboard) isAuthenticated(r *http.Request) bool {
	// If authentication is disabled, always return true
	if !getBoolValue(d.config.Dashboard.Auth.Enabled) {
		return true
	}

	// Get session cookie
	cookie, err := r.Cookie("session")
	if err != nil {
		return false
	}

	// Check if session exists and is valid
	d.sessionMu.RLock()
	expiry, exists := d.sessions[cookie.Value]
	d.sessionMu.RUnlock()

	if !exists {
		return false
	}

	// Check if session has expired
	if time.Now().After(expiry) {
		// Remove expired session
		d.sessionMu.Lock()
		delete(d.sessions, cookie.Value)
		d.sessionMu.Unlock()
		return false
	}

	return true
}

// generateSessionID generates a random session ID
func (d *Dashboard) generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// handleLogin handles login requests
func (d *Dashboard) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var loginData struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&loginData); err != nil {
		log.Printf("Error decoding login data: %v", err)
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	// Check credentials
	if loginData.Username == d.config.Dashboard.Auth.Username &&
		loginData.Password == d.config.Dashboard.Auth.Password {
		// Generate session token
		token := uuid.New().String()

		// Store session
		d.sessions[token] = time.Now().Add(24 * time.Hour) // 24 hour session

		// Set cookie
		http.SetCookie(w, &http.Cookie{
			Name:     "session",
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   86400, // 24 hours
		})

		// Return success response
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "success",
			"token":  token,
		})
		return
	}

	// Invalid credentials
	http.Error(w, "Invalid credentials", http.StatusUnauthorized)
}

// handleLogout handles logout requests
func (d *Dashboard) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get session cookie
	cookie, err := r.Cookie("session")
	if err == nil {
		// Clear the session from our map
		d.sessionMu.Lock()
		delete(d.sessions, cookie.Value)
		d.sessionMu.Unlock()

		// Clear the cookie by setting it to expire immediately
		http.SetCookie(w, &http.Cookie{
			Name:     "session",
			Value:    "",
			Path:     "/",
			Expires:  time.Unix(0, 0),
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteStrictMode,
		})
	}

	// Return success response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "Logged out successfully",
	})
}

// handleConfig handles configuration requests
func (d *Dashboard) handleConfig(w http.ResponseWriter, r *http.Request) {
	if !d.isAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		config, err := database.GetFullConfig()
		if err != nil {
			log.Printf("Error getting config: %v", err)
			http.Error(w, "Failed to get configuration", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(config)

	case http.MethodPost:
		var config database.Config
		if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
			log.Printf("Error decoding config: %v", err)
			http.Error(w, "Invalid configuration format", http.StatusBadRequest)
			return
		}

		if err := database.UpdateFullConfig(&config); err != nil {
			log.Printf("Error updating config: %v", err)
			http.Error(w, "Failed to update configuration", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Configuration updated successfully",
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleBackends handles backend requests
func (d *Dashboard) handleBackends(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(d.config.Backends)
	case http.MethodPost:
		var backend struct {
			URL            string `json:"url"`
			Weight         string `json:"weight"`
			MaxConnections string `json:"max_connections"`
		}
		if err := json.NewDecoder(r.Body).Decode(&backend); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}

		// Add backend
		d.config.Backends = append(d.config.Backends, struct {
			URL            string `yaml:"url"`
			Weight         string `yaml:"weight"`
			MaxConnections string `yaml:"max_connections"`
			Healthy        bool   `yaml:"healthy"`
		}{
			URL:            backend.URL,
			Weight:         backend.Weight,
			MaxConnections: backend.MaxConnections,
			Healthy:        true,
		})

		// Convert to []database.Backend for database update
		var dbBackends []database.Backend
		for _, b := range d.config.Backends {
			dbBackends = append(dbBackends, database.Backend{
				URL:            b.URL,
				Weight:         b.Weight,
				MaxConnections: b.MaxConnections,
				Healthy:        1, // true as int
			})
		}
		if err := database.UpdateBackends(dbBackends); err != nil {
			http.Error(w, "Failed to update backends in database", http.StatusInternalServerError)
			return
		}

		if err := d.saveConfig(d.config); err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		url := r.URL.Query().Get("url")
		if url == "" {
			http.Error(w, "Missing URL parameter", http.StatusBadRequest)
			return
		}

		// Remove backend
		var newBackends []struct {
			URL            string `yaml:"url"`
			Weight         string `yaml:"weight"`
			MaxConnections string `yaml:"max_connections"`
			Healthy        bool   `yaml:"healthy"`
		}
		for _, b := range d.config.Backends {
			if b.URL != url {
				newBackends = append(newBackends, b)
			}
		}
		d.config.Backends = newBackends

		// Convert to []database.Backend for database update
		var dbBackends []database.Backend
		for _, b := range d.config.Backends {
			dbBackends = append(dbBackends, database.Backend{
				URL:            b.URL,
				Weight:         b.Weight,
				MaxConnections: b.MaxConnections,
				Healthy:        1, // true as int
			})
		}
		if err := database.UpdateBackends(dbBackends); err != nil {
			http.Error(w, "Failed to update backends in database", http.StatusInternalServerError)
			return
		}

		if err := d.saveConfig(d.config); err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleMetrics handles metrics requests
func (d *Dashboard) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Fetch metrics from load balancer
	resp, err := http.Get("http://localhost:8080/metrics")
	if err != nil {
		http.Error(w, "Failed to fetch metrics", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "Failed to read metrics", http.StatusInternalServerError)
		return
	}

	// Parse metrics
	var metrics map[string]interface{}
	if err := json.Unmarshal(body, &metrics); err != nil {
		http.Error(w, "Failed to parse metrics", http.StatusInternalServerError)
		return
	}

	// Return metrics
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

// handleVerify handles token verification requests
func (d *Dashboard) handleVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get token from Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "No token provided"})
		return
	}

	// Extract token from "Bearer <token>"
	token := authHeader
	if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
		token = authHeader[7:]
	}

	// Check if session exists and is valid
	d.sessionMu.RLock()
	expiry, exists := d.sessions[token]
	d.sessionMu.RUnlock()

	if !exists || time.Now().After(expiry) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid or expired token"})
		return
	}

	// Return success response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "valid"})
}

// handleLoginPage serves the login page
func (d *Dashboard) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if d.isAuthenticated(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Load login template
	tmpl, err := template.ParseFiles("gui/templates/login.html")
	if err != nil {
		log.Printf("Error loading login template: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Execute template
	if err := tmpl.Execute(w, nil); err != nil {
		log.Printf("Error executing login template: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// handleIndex serves the main dashboard page
func (d *Dashboard) handleIndex(w http.ResponseWriter, r *http.Request) {
	if !d.isAuthenticated(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Add cache control headers to prevent caching
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	// Get current configuration
	config, err := database.GetFullConfig()
	if err != nil {
		log.Printf("Error getting config: %v", err)
		http.Error(w, "Failed to get configuration", http.StatusInternalServerError)
		return
	}

	// Execute template with config
	if err := d.template.Execute(w, map[string]interface{}{"Config": config}); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// handleReload reloads the configuration from the database
func (d *Dashboard) handleReload(w http.ResponseWriter, r *http.Request) {
	if !d.isAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Reload configuration from database
	dbConfig, err := database.GetFullConfig()
	if err != nil {
		log.Printf("Error reloading config: %v", err)
		http.Error(w, "Failed to reload configuration", http.StatusInternalServerError)
		return
	}

	// Convert *database.Config to *Config
	config := &Config{
		Server: struct {
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
		}{
			Port:        dbConfig.Server.Port,
			WorkerCount: dbConfig.Server.WorkerCount,
			TLS: struct {
				Enabled      string   `yaml:"enabled"`
				CertFile     string   `yaml:"cert_file"`
				KeyFile      string   `yaml:"key_file"`
				MinVersion   string   `yaml:"min_version"`
				CipherSuites []string `yaml:"cipher_suites"`
				ClientAuth   string   `yaml:"client_auth"`
			}{
				Enabled:      dbConfig.TLS.Enabled,
				CertFile:     dbConfig.TLS.CertFile,
				KeyFile:      dbConfig.TLS.KeyFile,
				MinVersion:   dbConfig.TLS.MinVersion,
				CipherSuites: dbConfig.TLS.CipherSuites,
				ClientAuth:   dbConfig.TLS.ClientAuth,
			},
			LoadBalancing: struct {
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
			}{
				Strategy: dbConfig.LoadBalancing.Strategy,
				IPHash: struct {
					Enabled        string `yaml:"enabled"`
					Header         string `yaml:"header"`
					FallbackHeader string `yaml:"fallback_header"`
				}{
					Enabled:        dbConfig.LoadBalancing.IPHash.Enabled,
					Header:         dbConfig.LoadBalancing.IPHash.Header,
					FallbackHeader: dbConfig.LoadBalancing.IPHash.FallbackHeader,
				},
				WeightedRoundRobin: struct {
					Enabled          string `yaml:"enabled"`
					WeightByCapacity string `yaml:"weight_by_capacity"`
				}{
					Enabled:          dbConfig.LoadBalancing.WeightedRoundRobin.Enabled,
					WeightByCapacity: dbConfig.LoadBalancing.WeightedRoundRobin.WeightByCapacity,
				},
			},
			ConnectionPool: struct {
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
			}{
				Enabled:               dbConfig.ConnectionPool.Enabled,
				MaxIdleConns:          dbConfig.ConnectionPool.MaxIdleConns,
				MaxIdleConnsPerHost:   dbConfig.ConnectionPool.MaxIdleConnsPerHost,
				MaxConnsPerHost:       dbConfig.ConnectionPool.MaxConnsPerHost,
				IdleConnTimeout:       dbConfig.ConnectionPool.IdleConnTimeout,
				MaxLifetime:           dbConfig.ConnectionPool.MaxLifetime,
				KeepAlive:             dbConfig.ConnectionPool.KeepAlive,
				DialTimeout:           dbConfig.ConnectionPool.DialTimeout,
				TLSHandshakeTimeout:   dbConfig.ConnectionPool.TLSHandshakeTimeout,
				ExpectContinueTimeout: dbConfig.ConnectionPool.ExpectContinueTimeout,
				ResponseHeaderTimeout: dbConfig.ConnectionPool.ResponseHeaderTimeout,
				DisableKeepAlives:     dbConfig.ConnectionPool.DisableKeepAlives == 1,
				DisableCompression:    dbConfig.ConnectionPool.DisableCompression == 1,
			},
			Cache: struct {
				Enabled      string   `yaml:"enabled"`
				TTL          string   `yaml:"ttl"`
				MaxSize      string   `yaml:"max_size"`
				Headers      []string `yaml:"headers"`
				ExcludePaths []string `yaml:"exclude_paths"`
			}{
				Enabled:      dbConfig.Cache.Enabled,
				TTL:          strconv.Itoa(dbConfig.Cache.TTL),
				MaxSize:      strconv.Itoa(dbConfig.Cache.MaxSize),
				Headers:      dbConfig.Cache.Headers,
				ExcludePaths: dbConfig.Cache.ExcludePaths,
			},
			CircuitBreaker: struct {
				Enabled          string `yaml:"enabled"`
				FailureThreshold string `yaml:"failure_threshold"`
				SuccessThreshold string `yaml:"success_threshold"`
				ResetTimeout     string `yaml:"reset_timeout"`
				HalfOpenTimeout  string `yaml:"half_open_timeout"`
			}{
				Enabled:          dbConfig.CircuitBreaker.Enabled,
				FailureThreshold: dbConfig.CircuitBreaker.FailureThreshold,
				SuccessThreshold: dbConfig.CircuitBreaker.SuccessThreshold,
				ResetTimeout:     dbConfig.CircuitBreaker.ResetTimeout,
				HalfOpenTimeout:  dbConfig.CircuitBreaker.HalfOpenTimeout,
			},
		},
		Metrics: struct {
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
		}{
			Enabled: dbConfig.Metrics.Enabled,
			Port:    dbConfig.Metrics.Port,
			Path:    dbConfig.Metrics.Path,
			Auth: struct {
				Enabled     bool   `yaml:"enabled"`
				BearerToken string `yaml:"bearer_token"`
			}{
				Enabled:     dbConfig.Metrics.AuthEnabled,
				BearerToken: dbConfig.Metrics.BearerToken,
			},
			CollectInterval: dbConfig.Metrics.CollectInterval,
			RetentionPeriod: dbConfig.Metrics.RetentionPeriod,
			Labels:          dbConfig.Metrics.Labels,
			Prometheus: struct {
				Enabled   string `yaml:"enabled"`
				Namespace string `yaml:"namespace"`
				Subsystem string `yaml:"subsystem"`
				Path      string `yaml:"path"`
			}{
				Enabled:   dbConfig.Metrics.Prometheus.Enabled,
				Namespace: dbConfig.Metrics.Prometheus.Namespace,
				Subsystem: dbConfig.Metrics.Prometheus.Subsystem,
				Path:      dbConfig.Metrics.Prometheus.Path,
			},
		},
		Backends: func() []struct {
			URL            string `yaml:"url"`
			Weight         string `yaml:"weight"`
			MaxConnections string `yaml:"max_connections"`
			Healthy        bool   `yaml:"healthy"`
		} {
			var backends []struct {
				URL            string `yaml:"url"`
				Weight         string `yaml:"weight"`
				MaxConnections string `yaml:"max_connections"`
				Healthy        bool   `yaml:"healthy"`
			}
			for _, b := range dbConfig.Backends {
				backends = append(backends, struct {
					URL            string `yaml:"url"`
					Weight         string `yaml:"weight"`
					MaxConnections string `yaml:"max_connections"`
					Healthy        bool   `yaml:"healthy"`
				}{
					URL:            b.URL,
					Weight:         b.Weight,
					MaxConnections: b.MaxConnections,
					Healthy:        b.Healthy == 1,
				})
			}
			return backends
		}(),
		Logging: struct {
			Enabled    string `yaml:"enabled"`
			Level      string `yaml:"level"`
			File       string `yaml:"file"`
			MaxSize    string `yaml:"max_size"`
			MaxBackups string `yaml:"max_backups"`
			MaxAge     string `yaml:"max_age"`
			Compress   string `yaml:"compress"`
			Format     string `yaml:"format"`
		}{
			Enabled:    dbConfig.Logging.Enabled,
			Level:      dbConfig.Logging.Level,
			File:       dbConfig.Logging.File,
			MaxSize:    dbConfig.Logging.MaxSize,
			MaxBackups: dbConfig.Logging.MaxBackups,
			MaxAge:     dbConfig.Logging.MaxAge,
			Compress:   dbConfig.Logging.Compress,
			Format:     dbConfig.Logging.Format,
		},
		Dashboard: struct {
			Port int `yaml:"port"`
			Auth struct {
				Enabled        string `yaml:"enabled"`
				Username       string `yaml:"username"`
				Password       string `yaml:"password"`
				SessionTimeout string `yaml:"session_timeout"`
			} `yaml:"auth"`
		}{
			Port: d.config.Dashboard.Port,
			Auth: struct {
				Enabled        string `yaml:"enabled"`
				Username       string `yaml:"username"`
				Password       string `yaml:"password"`
				SessionTimeout string `yaml:"session_timeout"`
			}{
				Enabled:        d.config.Dashboard.Auth.Enabled,
				Username:       d.config.Dashboard.Auth.Username,
				Password:       d.config.Dashboard.Auth.Password,
				SessionTimeout: d.config.Dashboard.Auth.SessionTimeout,
			},
		},
	}

	// Update in-memory config
	d.config = config

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Configuration reloaded successfully",
	})
}

// Start initializes and starts the dashboard server
func (d *Dashboard) Start() error {
	// Initialize database
	if err := database.InitDB("data/config.db"); err != nil {
		return fmt.Errorf("failed to initialize database: %v", err)
	}

	// Set up routes
	mux := http.NewServeMux()

	// Handle main page with authentication
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !d.isAuthenticated(r) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		d.handleIndex(w, r)
	})

	// Handle login page and API endpoints
	mux.HandleFunc("/login", d.handleLoginPage)
	mux.HandleFunc("/api/login", d.handleLogin)
	mux.HandleFunc("/api/logout", d.handleLogout)
	mux.HandleFunc("/api/verify", d.handleVerify)
	mux.HandleFunc("/api/config", d.handleConfig)
	mux.HandleFunc("/api/backends", d.handleBackends)
	mux.HandleFunc("/api/metrics", d.handleMetrics)
	mux.HandleFunc("/api/reload", d.handleReload)

	// Serve static files
	fs := http.FileServer(http.Dir("gui/static"))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))

	// Start server
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", d.config.Dashboard.Port),
		Handler: mux,
	}

	log.Printf("Dashboard server starting on port %d", d.config.Dashboard.Port)
	return server.ListenAndServe()
}

// getBoolValue converts a string value to bool
func getBoolValue(s string) bool {
	return s == "true" || s == "on" || s == "1"
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Server.Port == "" {
		return fmt.Errorf("server port is required")
	}
	if c.Server.WorkerCount == "" {
		return fmt.Errorf("worker count is required")
	}
	if getBoolValue(c.Server.TLS.Enabled) {
		if c.Server.TLS.CertFile == "" {
			return fmt.Errorf("certificate file is required when TLS is enabled")
		}
		if c.Server.TLS.KeyFile == "" {
			return fmt.Errorf("key file is required when TLS is enabled")
		}
	}
	if c.Server.LoadBalancing.Strategy == "" {
		return fmt.Errorf("load balancing strategy is required")
	}
	if c.Server.ConnectionPool.Enabled == "" {
		return fmt.Errorf("connection pool enabled setting is required")
	}
	if c.Server.Cache.Enabled == "" {
		return fmt.Errorf("cache enabled setting is required")
	}
	if c.Server.CircuitBreaker.Enabled == "" {
		return fmt.Errorf("circuit breaker enabled setting is required")
	}
	if getBoolValue(c.Metrics.Enabled) {
		if c.Metrics.Port == "" {
			return fmt.Errorf("metrics port is required when metrics are enabled")
		}
		if c.Metrics.Path == "" {
			return fmt.Errorf("metrics path is required when metrics are enabled")
		}
		if c.Metrics.CollectInterval == "" {
			return fmt.Errorf("collect interval is required when metrics are enabled")
		}
		if c.Metrics.RetentionPeriod == "" {
			return fmt.Errorf("retention period is required when metrics are enabled")
		}
	}
	if getBoolValue(c.Logging.Enabled) {
		if c.Logging.Level == "" {
			return fmt.Errorf("log level is required when logging is enabled")
		}
		if c.Logging.File == "" {
			return fmt.Errorf("log file is required when logging is enabled")
		}
		if c.Logging.MaxSize == "" {
			return fmt.Errorf("max size is required when logging is enabled")
		}
		if c.Logging.MaxBackups == "" {
			return fmt.Errorf("max backups is required when logging is enabled")
		}
		if c.Logging.MaxAge == "" {
			return fmt.Errorf("max age is required when logging is enabled")
		}
	}
	if len(c.Backends) == 0 {
		return fmt.Errorf("at least one backend is required")
	}
	for i, backend := range c.Backends {
		if backend.URL == "" {
			return fmt.Errorf("backend %d: URL is required", i+1)
		}
		if backend.Weight == "" {
			return fmt.Errorf("backend %d: weight is required", i+1)
		}
		if backend.MaxConnections == "" {
			return fmt.Errorf("backend %d: max connections is required", i+1)
		}
	}
	return nil
}
