package main

import (
	"fmt"
	"log"
	"os"

	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/api"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/database"
	"github.com/joho/godotenv"
)

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	fmt.Println("Démarrage du moteur CI/CD...")

	// Initialize database connection
	db, err := database.New()
	if err != nil {
		log.Printf("Warning: Could not connect to database: %v", err)
		log.Println("Running without database persistence...")
		db = nil
	} else {
		defer db.Close()
		log.Println("Connected to database successfully")
	}

	// Get port from environment or use default
	port := os.Getenv("API_PORT")
	if port == "" {
		port = "8080"
	}

	// Create and start the API server
	server, err := api.NewServer(db, port)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	log.Printf("CI/CD Engine ready!")
	log.Printf("Webhook endpoint: http://localhost:%s/webhook/github", port)
	log.Printf("Health check: http://localhost:%s/health", port)

	// Start the server (this blocks)
	if err := server.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}