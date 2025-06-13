package gui

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
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
		Port int `yaml:"port"`
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
	config      *Config
	sessions    map[string]time.Time
	sessionMu   sync.RWMutex
	template    *template.Template
	configPath  string
	configMutex sync.RWMutex
	guiFiles    embed.FS
}

// NewDashboard creates a new dashboard instance
func NewDashboard(config *Config, guiFiles embed.FS) *Dashboard {
	// Initialize sessions map
	sessions := make(map[string]time.Time)

	// Create template with function map
	funcMap := template.FuncMap{
		"join": strings.Join,
	}

	// Parse templates from embedded files
	tmpl := template.Must(template.New("").Funcs(funcMap).ParseFS(guiFiles,
		"gui/templates/login.html",
		"gui/templates/index.html",
	))

	// Create dashboard with config and embedded files
	dashboard := &Dashboard{
		config:     config,
		sessions:   sessions,
		template:   tmpl,
		configPath: "config.yml",
		guiFiles:   guiFiles,
	}

	return dashboard
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
		// Clean up expired session
		d.sessionMu.Lock()
		delete(d.sessions, cookie.Value)
		d.sessionMu.Unlock()
		return false
	}

	// Extend session expiry
	d.sessionMu.Lock()
	d.sessions[cookie.Value] = time.Now().Add(24 * time.Hour)
	d.sessionMu.Unlock()

	return true
}

// getBoolValue converts a string to a boolean value
func getBoolValue(s string) bool {
	return s == "true" || s == "1" || s == "yes" || s == "y"
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
		token, err := d.generateSessionID()
		if err != nil {
			log.Printf("Error generating session ID: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Store session
		d.sessionMu.Lock()
		d.sessions[token] = time.Now().Add(24 * time.Hour) // 24 hour session
		d.sessionMu.Unlock()

		// Set cookie
		http.SetCookie(w, &http.Cookie{
			Name:     "session",
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			Secure:   false,                // Allow non-HTTPS for local development
			SameSite: http.SameSiteLaxMode, // More permissive SameSite policy
			MaxAge:   86400,                // 24 hours
		})

		// Return success response with redirect URL
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":   "success",
			"token":    token,
			"redirect": "/dashboard",
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
	if err != nil {
		log.Printf("No session cookie found: %v", err)
	} else {
		// Clear the session from our map
		d.sessionMu.Lock()
		delete(d.sessions, cookie.Value)
		d.sessionMu.Unlock()

		// Clear the cookie
		http.SetCookie(w, &http.Cookie{
			Name:     "session",
			Value:    "",
			Path:     "/",
			Expires:  time.Unix(0, 0),
			HttpOnly: true,
			Secure:   false,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
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

	if r.Method == http.MethodGet {
		// Return current config
		d.configMutex.RLock()
		config := d.config
		d.configMutex.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(config); err != nil {
			log.Printf("Error encoding config: %v", err)
			http.Error(w, "Failed to encode configuration", http.StatusInternalServerError)
			return
		}
		return
	}

	// Handle POST request
	var newConfig Config
	if err := json.NewDecoder(r.Body).Decode(&newConfig); err != nil {
		log.Printf("Error decoding config: %v", err)
		http.Error(w, "Invalid configuration", http.StatusBadRequest)
		return
	}

	// Update in-memory config
	d.configMutex.Lock()
	d.config = &newConfig
	d.configMutex.Unlock()

	// Save to file
	if err := d.saveConfig(&newConfig); err != nil {
		log.Printf("Error saving config: %v", err)
		http.Error(w, "Failed to save configuration", http.StatusInternalServerError)
		return
	}

	// Return success response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "Configuration updated successfully",
	})
}

// handleBackends handles backend requests
func (d *Dashboard) handleBackends(w http.ResponseWriter, r *http.Request) {
	if !d.isAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

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
	if !d.isAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

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

// generateSessionID generates a random session ID
func (d *Dashboard) generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
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

// handleLoginPage serves the login page
func (d *Dashboard) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if d.isAuthenticated(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Execute template from embedded files
	if err := d.template.ExecuteTemplate(w, "login.html", nil); err != nil {
		log.Printf("Error executing login template: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// handleIndex handles the main dashboard page
func (d *Dashboard) handleIndex(w http.ResponseWriter, r *http.Request) {
	// Add debug logging
	log.Printf("Handling index request for path: %s", r.URL.Path)

	if !d.isAuthenticated(r) {
		log.Printf("User not authenticated, redirecting to login")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Add cache control headers to prevent caching of the page itself
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	// Debug log the config being passed to the template
	log.Printf("Passing configuration to template: %+v", d.config)

	// Create a template data struct that includes both the config and any additional data needed
	templateData := struct {
		Config *Config
		JSON   string
	}{
		Config: d.config,
		JSON:   "", // Will be populated with JSON string
	}

	// Convert config to JSON for JavaScript
	jsonData, err := json.Marshal(d.config)
	if err != nil {
		log.Printf("Error marshaling config to JSON: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	templateData.JSON = string(jsonData)

	// Execute the template with the data
	if err := d.template.ExecuteTemplate(w, "index.html", templateData); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// Start starts the dashboard server
func (d *Dashboard) Start() error {
	// Set up routes
	mux := http.NewServeMux()

	// Serve static files from embedded filesystem
	staticFS, err := fs.Sub(d.guiFiles, "gui/static")
	if err != nil {
		return fmt.Errorf("failed to create static filesystem: %v", err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// API routes
	mux.HandleFunc("/api/login", d.handleLogin)
	mux.HandleFunc("/api/logout", d.handleLogout)
	mux.HandleFunc("/api/config", d.handleConfig)
	mux.HandleFunc("/api/backends", d.handleBackends)
	mux.HandleFunc("/api/metrics", d.handleMetrics)
	mux.HandleFunc("/api/verify", d.handleVerify)

	// Page routes
	mux.HandleFunc("/login", d.handleLoginPage)
	mux.HandleFunc("/dashboard", d.handleIndex)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Redirect root to dashboard if authenticated, otherwise to login
		if d.isAuthenticated(r) {
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		} else {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
		}
	})

	// Start server
	addr := fmt.Sprintf(":%d", d.config.Dashboard.Port)
	log.Printf("Starting dashboard server on %s", addr)
	return http.ListenAndServe(addr, mux)
}

// LoadConfig loads the configuration from a YAML file
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
