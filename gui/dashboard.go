package gui

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

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
		Auth struct {
			Enabled        string `yaml:"enabled"`
			Username       string `yaml:"username"`
			Password       string `yaml:"password"`
			SessionTimeout string `yaml:"session_timeout"`
		} `yaml:"auth"`
	} `yaml:"dashboard"`
}

// Dashboard represents the dashboard instance
type Dashboard struct {
	config     *Config
	sessions   map[string]time.Time
	sessionMu  sync.RWMutex
	template   *template.Template
	configPath string
}

// NewDashboard creates a new dashboard instance
func NewDashboard(configPath string) (*Dashboard, error) {
	// Load configuration
	config, err := loadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %v", err)
	}

	// Load HTML template
	tmpl, err := template.ParseFiles("gui/templates/index.html")
	if err != nil {
		return nil, fmt.Errorf("failed to load template: %v", err)
	}

	return &Dashboard{
		config:     config,
		sessions:   make(map[string]time.Time),
		template:   tmpl,
		configPath: configPath,
	}, nil
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

	// Set default values if not provided
	if config.Dashboard.Auth.SessionTimeout == "" {
		config.Dashboard.Auth.SessionTimeout = "24h"
	}

	return &config, nil
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

	// Parse form
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Get credentials
	username := r.Form.Get("username")
	password := r.Form.Get("password")

	// Check credentials
	if username != d.config.Dashboard.Auth.Username || password != d.config.Dashboard.Auth.Password {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	// Generate session ID
	sessionID, err := d.generateSessionID()
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Parse session timeout
	timeout, err := time.ParseDuration(d.config.Dashboard.Auth.SessionTimeout)
	if err != nil {
		timeout = 24 * time.Hour // Default to 24 hours
	}

	// Store session
	d.sessionMu.Lock()
	d.sessions[sessionID] = time.Now().Add(timeout)
	d.sessionMu.Unlock()

	// Set session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().Add(timeout),
	})

	// Redirect to dashboard
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleLogout handles logout requests
func (d *Dashboard) handleLogout(w http.ResponseWriter, r *http.Request) {
	// Get session cookie
	cookie, err := r.Cookie("session")
	if err == nil {
		// Remove session
		d.sessionMu.Lock()
		delete(d.sessions, cookie.Value)
		d.sessionMu.Unlock()
	}

	// Clear session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().Add(-time.Hour),
	})

	// Redirect to login page
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// handleConfig handles configuration requests
func (d *Dashboard) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(d.config)
	case http.MethodPost:
		var newConfig Config
		if err := json.NewDecoder(r.Body).Decode(&newConfig); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}

		// Update configuration
		d.config = &newConfig
		if err := d.saveConfig(d.config); err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
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

// Start starts the dashboard server
func (d *Dashboard) Start(addr string) error {
	// Create router
	mux := http.NewServeMux()

	// Serve static files
	fs := http.FileServer(http.Dir("gui/static"))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))

	// Handle login page
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.ServeFile(w, r, "gui/templates/login.html")
		} else if r.Method == http.MethodPost {
			d.handleLogin(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Handle logout
	mux.HandleFunc("/logout", d.handleLogout)

	// Handle API endpoints
	mux.HandleFunc("/api/config", d.handleConfig)
	mux.HandleFunc("/api/backends", d.handleBackends)
	mux.HandleFunc("/api/metrics", d.handleMetrics)

	// Handle main page with authentication
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Check authentication
		if !d.isAuthenticated(r) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Create template data
		data := struct {
			Config *Config
		}{
			Config: d.config,
		}

		// Check if template is valid
		if d.template == nil {
			http.Error(w, "Internal server error: template not loaded", http.StatusInternalServerError)
			return
		}

		// Serve dashboard with configuration data
		if err := d.template.Execute(w, data); err != nil {
			// Log the error but don't try to write another response
			log.Printf("Template execution error: %v", err)
			return
		}
	})

	// Create server
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Start server
	return server.ListenAndServe()
}

// getBoolValue converts a string value to bool
func getBoolValue(s string) bool {
	return s == "true" || s == "on" || s == "1"
}
