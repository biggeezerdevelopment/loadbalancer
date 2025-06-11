package testbackend

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

// Server represents a test backend server
type Server struct {
	port          string
	requestCount  uint64
	errorCount    uint64
	startTime     time.Time
	healthy       bool
	responseDelay time.Duration
}

// NewServer creates a new test backend server
func NewServer(port string) *Server {
	return &Server{
		port:          port,
		startTime:     time.Now(),
		healthy:       true,
		responseDelay: 100 * time.Millisecond,
	}
}

// Start starts the test backend server
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if !s.healthy {
			http.Error(w, "Unhealthy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Metrics endpoint
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		uptime := time.Since(s.startTime)
		metrics := map[string]interface{}{
			"uptime_seconds": uptime.Seconds(),
			"request_count":  atomic.LoadUint64(&s.requestCount),
			"error_count":    atomic.LoadUint64(&s.errorCount),
			"healthy":        s.healthy,
		}
		json.NewEncoder(w).Encode(metrics)
	})

	// Test endpoint that simulates work
	mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddUint64(&s.requestCount, 1)

		// Simulate some work
		time.Sleep(s.responseDelay)

		// Randomly return errors (10% chance)
		/*
			if atomic.LoadUint64(&s.requestCount)%10 == 0 {
				atomic.AddUint64(&s.errorCount, 1)
				http.Error(w, "Random error occurred", http.StatusInternalServerError)
				return
			}
		*/
		response := map[string]interface{}{
			"message":    "Hello from test backend",
			"backend":    s.port,
			"timestamp":  time.Now().Format(time.RFC3339),
			"request_id": r.Header.Get("X-Request-ID"),
			"trace_id":   r.Header.Get("X-Trace-ID"),
			"span_id":    r.Header.Get("X-Span-ID"),
		}
		json.NewEncoder(w).Encode(response)
	})

	// Toggle health endpoint (for testing load balancer behavior)
	mux.HandleFunc("/toggle-health", func(w http.ResponseWriter, r *http.Request) {
		s.healthy = !s.healthy
		response := map[string]interface{}{
			"healthy": s.healthy,
			"port":    s.port,
		}
		json.NewEncoder(w).Encode(response)
	})

	// Set response delay endpoint
	mux.HandleFunc("/set-delay", func(w http.ResponseWriter, r *http.Request) {
		delay := r.URL.Query().Get("delay")
		if delay != "" {
			if d, err := time.ParseDuration(delay); err == nil {
				s.responseDelay = d
			}
		}
		response := map[string]interface{}{
			"delay": s.responseDelay.String(),
			"port":  s.port,
		}
		json.NewEncoder(w).Encode(response)
	})

	// Favicon handler to prevent 503 errors
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	// Create server
	server := &http.Server{
		Addr:    ":" + s.port,
		Handler: mux,
	}

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Printf("Shutting down test backend on port %s...", s.port)
		server.Close()
	}()

	log.Printf("Starting test backend on port %s", s.port)
	return server.ListenAndServe()
}
