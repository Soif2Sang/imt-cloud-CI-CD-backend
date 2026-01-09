package pipeline

import (
	"os"
	"testing"
)

func TestParse(t *testing.T) {
	// Create a temporary valid pipeline file
	validContent := `
stages:
  - build
  - test
jobs:
  build-job:
    stage: build
    image: golang:1.21
    script:
      - go build ./...
`
	tmpFile, err := os.CreateTemp("", "pipeline-*.yml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(validContent); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}

	// Test case 1: Valid file
	t.Run("ValidFile", func(t *testing.T) {
		parser := NewParser(tmpFile.Name())
		config, err := parser.Parse()
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		if len(config.Stages) != 2 {
			t.Errorf("Expected 2 stages, got %d", len(config.Stages))
		}
		if len(config.Jobs) != 1 {
			t.Errorf("Expected 1 job, got %d", len(config.Jobs))
		}
		
		job, ok := config.Jobs["build-job"]
		if !ok {
			t.Errorf("Expected job 'build-job' to exist")
		}
		if job.Stage != "build" {
			t.Errorf("Expected job stage 'build', got '%s'", job.Stage)
		}
	})

	// Test case 2: Non-existent file
	t.Run("NonExistentFile", func(t *testing.T) {
		parser := NewParser("non-existent-file.yml")
		_, err := parser.Parse()
		if err == nil {
			t.Error("Expected error for non-existent file, got nil")
		}
	})

	// Test case 3: Invalid YAML
	t.Run("InvalidYAML", func(t *testing.T) {
		invalidTmpFile, err := os.CreateTemp("", "invalid-pipeline-*.yml")
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}
		defer os.Remove(invalidTmpFile.Name())
		
		if _, err := invalidTmpFile.WriteString("invalid: [ yaml"); err != nil {
			t.Fatalf("Failed to write to temp file: %v", err)
		}
		invalidTmpFile.Close()

		parser := NewParser(invalidTmpFile.Name())
		_, err = parser.Parse()
		if err == nil {
			t.Error("Expected error for invalid YAML, got nil")
		}
	})
}
