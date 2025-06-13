package main

import (
	"embed"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"loadbalancer/internal/gui"
	"loadbalancer/internal/loadbalancer"
)

//go:embed "gui/templates" "gui/static"
var guiFiles embed.FS

func main() {
	// Parse command line flags
	configPath := flag.String("config", "config.yml", "Path to configuration file")
	flag.Parse()

	// Create load balancer instance
	lb, err := loadbalancer.NewLoadBalancer(*configPath)
	if err != nil {
		log.Fatalf("Failed to create load balancer: %v", err)
	}

	// Load configuration for dashboard
	config, err := gui.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create dashboard instance with embedded files
	dashboard := gui.NewDashboard(config, guiFiles)

	// Start dashboard server
	go func() {
		if err := dashboard.Start(); err != nil {
			log.Fatalf("Failed to start dashboard: %v", err)
		}
	}()

	// Start load balancer
	if err := lb.Start(); err != nil {
		log.Fatalf("Failed to start load balancer: %v", err)
	}

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nShutting down...")
}
