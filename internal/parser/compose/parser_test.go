package compose

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseServices(t *testing.T) {
	content := `
services:
  backend:
    build: .
  database:
    image: postgres
  frontend:
    build: ./frontend
`
	tmpFile, err := os.CreateTemp("", "docker-compose-*.yml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	services, err := ParseServices(tmpFile.Name())
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(services) != 2 {
		t.Errorf("Expected 2 buildable services, got %d", len(services))
	}

	// Check if backend and frontend are in the list
	foundBackend := false
	foundFrontend := false
	for _, s := range services {
		if s == "backend" {
			foundBackend = true
		}
		if s == "frontend" {
			foundFrontend = true
		}
	}

	if !foundBackend || !foundFrontend {
		t.Errorf("Expected backend and frontend to be buildable, got %v", services)
	}
}

func TestGenerateOverride(t *testing.T) {
	services := []string{"api", "web"}
	registryUser := "testuser"
	projectName := "Test Project"
	tag := "v1.0.0"

	overrideBytes, err := GenerateOverride(services, registryUser, projectName, tag)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	var override map[string]interface{}
	if err := yaml.Unmarshal(overrideBytes, &override); err != nil {
		t.Fatalf("Failed to parse generated override YAML: %v", err)
	}

	servicesMap, ok := override["services"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected 'services' key in override")
	}

	// Check api service
	apiConfig, ok := servicesMap["api"].(map[string]interface{})
	if !ok {
		t.Errorf("Expected 'api' service config")
	}
	expectedApiImage := "testuser/test-project-api:v1.0.0"
	if apiConfig["image"] != expectedApiImage {
		t.Errorf("Expected image '%s', got '%s'", expectedApiImage, apiConfig["image"])
	}

	// Check web service
	webConfig, ok := servicesMap["web"].(map[string]interface{})
	if !ok {
		t.Errorf("Expected 'web' service config")
	}
	expectedWebImage := "testuser/test-project-web:v1.0.0"
	if webConfig["image"] != expectedWebImage {
		t.Errorf("Expected image '%s', got '%s'", expectedWebImage, webConfig["image"])
	}
}

func TestGetContainerNames(t *testing.T) {
	content := `
services:
  app:
    container_name: my-app
  db:
    container_name: my-db
  redis:
    image: redis
`
	tmpFile, err := os.CreateTemp("", "docker-compose-names-*.yml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	names, err := GetContainerNames(tmpFile.Name())
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(names) != 2 {
		t.Errorf("Expected 2 container names, got %d", len(names))
	}

	foundApp := false
	foundDb := false
	for _, n := range names {
		if n == "my-app" {
			foundApp = true
		}
		if n == "my-db" {
			foundDb = true
		}
	}

	if !foundApp || !foundDb {
		t.Errorf("Expected my-app and my-db, got %v", names)
	}
}
