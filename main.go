package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"loadbalancer/gui"
)

func main() {
	// Create load balancer with configuration
	lb, err := NewLoadBalancer("config.yml")
	if err != nil {
		log.Fatalf("Failed to create load balancer: %v", err)
	}

	// Create dashboard
	dashboard, err := gui.NewDashboard("config.yml")
	if err != nil {
		log.Fatalf("Failed to create dashboard: %v", err)
	}

	// Start dashboard in a goroutine
	go func() {
		if err := dashboard.Start(":8081"); err != nil {
			log.Printf("Dashboard error: %v", err)
		}
	}()

	// Start load balancer in a goroutine
	go func() {
		if err := lb.Start(); err != nil {
			log.Printf("Load balancer error: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	// Gracefully shutdown
	log.Println("Shutting down...")
	if err := lb.Stop(); err != nil {
		log.Printf("Error stopping load balancer: %v", err)
	}
}
