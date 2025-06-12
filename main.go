package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"loadbalancer/gui"
	"loadbalancer/internal/database"
)

func main() {
	// Parse command line flags
	configPath := flag.String("config", "config.yml", "Path to configuration file")
	flag.Parse()

	// Initialize database
	if err := database.InitDB("data/config.db"); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.CloseDB()

	// Load initial configuration
	config, err := gui.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create and start dashboard
	dashboard := gui.NewDashboard(config)
	go func() {
		if err := dashboard.Start(); err != nil {
			log.Fatalf("Dashboard server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nShutting down...")
}
