package main

import (
	"os"

	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/api"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/database"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/pkg/logger"
	"github.com/joho/godotenv"
)

func main() {
	// Initialize Logger
	logger.Init()

	// Load .env file
	if err := godotenv.Load(); err != nil {
		logger.Warn("No .env file found, using system environment variables")
	}

	logger.Info("Démarrage du moteur CI/CD...")

	// Initialize database connection
	db, err := database.New(os.Getenv("ENCRYPTION_KEY"))
	if err != nil {
		logger.Warn("Warning: Could not connect to database: " + err.Error())
		logger.Warn("Running without database persistence...")
		db = nil
	} else {
		defer db.Close()
		logger.Info("Connected to database successfully")
	}

	// Get port from environment or use default
	port := os.Getenv("API_PORT")
	if port == "" {
		port = "8080"
	}

	// Create and start the API server
	server, err := api.NewServer(db, port)
	if err != nil {
		logger.Error("Failed to create server: " + err.Error())
		os.Exit(1)
	}

	logger.Info("CI/CD Engine ready!")
	logger.Info("Webhook endpoint: http://localhost:" + port + "/webhook/github")
	logger.Info("Health check: http://localhost:" + port + "/health")

	// Start the server (this blocks)
	if err := server.Start(); err != nil {
		logger.Error("Server error: " + err.Error())
		os.Exit(1)
	}
}
