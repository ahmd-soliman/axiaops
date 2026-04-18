//go:build integration

package integration

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// Check if we should manage the stack
	manageStack := os.Getenv("INTEGRATION_MANAGE_STACK") != "false"
	
	var apiURL, redisURL, ingestionURL string
	
	if manageStack {
		// Start Docker Compose
		if err := startStack(); err != nil {
			log.Fatalf("Failed to start stack: %v", err)
		}
		defer func() {
			if err := stopStack(); err != nil {
				log.Printf("Failed to stop stack: %v", err)
			}
		}()
		
		// Use default URLs
		apiURL = "http://localhost:8080"
		redisURL = "redis://localhost:6379"
		ingestionURL = "http://localhost:8081"
		
		// Wait for services to be ready
		if err := waitForServices(apiURL, ingestionURL); err != nil {
			log.Fatalf("Services not ready: %v", err)
		}
	} else {
		// Use provided URLs
		apiURL = os.Getenv("INTEGRATION_API_URL")
		redisURL = os.Getenv("INTEGRATION_REDIS_URL")
		ingestionURL = os.Getenv("INTEGRATION_INGESTION_URL")
	}
	
	// Set environment variables for tests
	os.Setenv("INTEGRATION_API_URL", apiURL)
	os.Setenv("INTEGRATION_REDIS_URL", redisURL)
	os.Setenv("INTEGRATION_INGESTION_URL", ingestionURL)
	
	// Run tests
	os.Exit(m.Run())
}

func startStack() error {
	dir := filepath.Join("..", "..", "test-infra", "integration")
	cmd := exec.Command("docker-compose", "up", "-d", "--force-recreate")
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func stopStack() error {
	dir := filepath.Join("..", "..", "test-infra", "integration")
	cmd := exec.Command("docker-compose", "down", "-v", "--remove-orphans")
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func waitForServices(apiURL, ingestionURL string) error {
	// Wait for API
	for i := 0; i < 30; i++ {
		resp, err := http.Get(apiURL + "/health")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			break
		}
		if i == 29 {
			return fmt.Errorf("API not ready after 30s")
		}
		time.Sleep(1 * time.Second)
	}
	
	// Wait for ingestion
	for i := 0; i < 30; i++ {
		resp, err := http.Get(ingestionURL + "/health")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			return nil
		}
		if i == 29 {
			return fmt.Errorf("Ingestion not ready after 30s")
		}
		time.Sleep(1 * time.Second)
	}
	
	return nil
}