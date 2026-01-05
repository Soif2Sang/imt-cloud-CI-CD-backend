package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/database"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/executor"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/git"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/models"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/parser"
)

// Server represents the API server
type Server struct {
	db     *database.DB
	docker *executor.DockerExecutor
	port   string
}

// NewServer creates a new API server
func NewServer(db *database.DB, port string) (*Server, error) {
	docker, err := executor.NewDockerExecutor()
	if err != nil {
		return nil, fmt.Errorf("failed to create docker executor: %w", err)
	}

	return &Server{
		db:     db,
		docker: docker,
		port:   port,
	}, nil
}

// Start starts the API server
func (s *Server) Start() error {
	// Health check
	http.HandleFunc("/health", s.handleHealth)

	// Webhook
	http.HandleFunc("/webhook/github", s.handleGitHubWebhook)

	// API v1 routes
	http.HandleFunc("/api/v1/projects", s.handleProjects)
	http.HandleFunc("/api/v1/projects/", s.routeProjectsSubpath)

	log.Printf("Starting API server on port %s", s.port)
	log.Printf("Endpoints:")
	log.Printf("  - GET    /health")
	log.Printf("  - POST   /webhook/github")
	log.Printf("  - GET    /api/v1/projects")
	log.Printf("  - POST   /api/v1/projects")
	log.Printf("  - GET    /api/v1/projects/{id}")
	log.Printf("  - PUT    /api/v1/projects/{id}")
	log.Printf("  - DELETE /api/v1/projects/{id}")
	log.Printf("  - GET    /api/v1/projects/{id}/pipelines")
	log.Printf("  - POST   /api/v1/projects/{id}/pipelines")
	log.Printf("  - GET    /api/v1/projects/{id}/pipelines/{id}")
	log.Printf("  - GET    /api/v1/projects/{id}/pipelines/{id}/jobs")
	log.Printf("  - GET    /api/v1/projects/{id}/pipelines/{id}/jobs/{id}")
	log.Printf("  - GET    /api/v1/projects/{id}/pipelines/{id}/jobs/{id}/logs")

	return http.ListenAndServe(":"+s.port, nil)
}

// routeProjectsSubpath routes requests under /api/v1/projects/
func (s *Server) routeProjectsSubpath(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/projects/")
	parts := strings.Split(path, "/")

	// /api/v1/projects/{projectId}
	if len(parts) == 1 && parts[0] != "" {
		s.handleProject(w, r)
		return
	}

	// /api/v1/projects/{projectId}/pipelines
	if len(parts) == 2 && parts[1] == "pipelines" {
		s.handlePipelines(w, r)
		return
	}

	// /api/v1/projects/{projectId}/pipelines/{pipelineId}
	if len(parts) == 3 && parts[1] == "pipelines" {
		s.handlePipeline(w, r)
		return
	}

	// /api/v1/projects/{projectId}/pipelines/{pipelineId}/jobs
	if len(parts) == 4 && parts[1] == "pipelines" && parts[3] == "jobs" {
		s.handleJobs(w, r)
		return
	}

	// /api/v1/projects/{projectId}/pipelines/{pipelineId}/jobs/{jobId}
	if len(parts) == 5 && parts[1] == "pipelines" && parts[3] == "jobs" {
		s.handleJob(w, r)
		return
	}

	// /api/v1/projects/{projectId}/pipelines/{pipelineId}/jobs/{jobId}/logs
	if len(parts) == 6 && parts[1] == "pipelines" && parts[3] == "jobs" && parts[5] == "logs" {
		s.handleLogs(w, r)
		return
	}

	respondError(w, http.StatusNotFound, "Not found")
}

// handleHealth is a simple health check endpoint
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleGitHubWebhook handles incoming GitHub push webhooks
func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check GitHub event type
	eventType := r.Header.Get("X-GitHub-Event")
	if eventType != "push" {
		log.Printf("Ignoring non-push event: %s", eventType)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"message": "event ignored"})
		return
	}

	// Parse the push event
	var pushEvent PushEvent
	if err := json.NewDecoder(r.Body).Decode(&pushEvent); err != nil {
		log.Printf("Failed to parse webhook payload: %v", err)
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	// Ignore branch deletions
	if pushEvent.Deleted {
		log.Printf("Ignoring branch deletion event")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"message": "deletion ignored"})
		return
	}

	// Extract branch name from ref (refs/heads/main -> main)
	branch := strings.TrimPrefix(pushEvent.Ref, "refs/heads/")
	commitHash := pushEvent.After

	log.Printf("Received push event for %s on branch %s (commit: %s)",
		pushEvent.Repository.FullName, branch, commitHash[:8])

	// Run pipeline asynchronously
	go s.runPipeline(pushEvent, branch, commitHash)

	// Respond immediately
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Pipeline triggered",
		"branch":  branch,
		"commit":  commitHash,
	})
}

// runPipeline executes the CI/CD pipeline
func (s *Server) runPipeline(pushEvent PushEvent, branch, commitHash string) {
	repoURL := pushEvent.Repository.CloneURL
	repoName := pushEvent.Repository.Name

	// Create a unique workspace directory
	workspaceDir := filepath.Join("/tmp", "cicd-workspaces", fmt.Sprintf("%s-%s-%d", repoName, commitHash[:8], time.Now().Unix()))

	log.Printf("Starting pipeline for %s", pushEvent.Repository.FullName)

	// Find or create project in database
	var projectID int
	var accessToken string

	if s.db != nil {
		project, err := s.findOrCreateProject(pushEvent.Repository)
		if err != nil {
			log.Printf("Failed to find/create project: %v", err)
		} else {
			projectID = project.ID
			accessToken = project.AccessToken
		}
	}

	// Create pipeline record
	var pipelineID int
	if s.db != nil && projectID > 0 {
		pipeline, err := s.db.CreatePipeline(projectID, branch, commitHash)
		if err != nil {
			log.Printf("Failed to create pipeline record: %v", err)
		} else {
			pipelineID = pipeline.ID
			log.Printf("Pipeline created with ID: %d", pipelineID)

			// Update status to running
			s.db.UpdatePipelineStatus(pipelineID, "running")
		}
	}

	// Clone the repository
	log.Printf("Cloning repository to %s", workspaceDir)
	if err := git.Clone(repoURL, branch, workspaceDir, accessToken, commitHash); err != nil {
		log.Printf("Failed to clone repository: %v", err)
		if s.db != nil && pipelineID > 0 {
			s.db.UpdatePipelineStatus(pipelineID, "failed")
		}
		return
	}
	defer git.Cleanup(workspaceDir)

	// Find and parse the CI config file
	configPath := s.findCIConfig(workspaceDir)
	if configPath == "" {
		log.Printf("No CI config file found in repository")
		if s.db != nil && pipelineID > 0 {
			s.db.UpdatePipelineStatus(pipelineID, "failed")
		}
		return
	}

	log.Printf("Found CI config: %s", configPath)

	// Parse the CI config
	p := parser.NewParser(configPath)
	config, err := p.Parse()
	if err != nil {
		log.Printf("Failed to parse CI config: %v", err)
		if s.db != nil && pipelineID > 0 {
			s.db.UpdatePipelineStatus(pipelineID, "failed")
		}
		return
	}

	log.Printf("Config loaded with %d stages", len(config.Stages))

	// Execute the pipeline
	pipelineSuccess := s.executePipeline(config, workspaceDir, pipelineID)

	// Update final pipeline status
	if s.db != nil && pipelineID > 0 {
		if pipelineSuccess {
			s.db.UpdatePipelineStatus(pipelineID, "success")
			log.Printf("Pipeline %d completed successfully", pipelineID)
		} else {
			s.db.UpdatePipelineStatus(pipelineID, "failed")
			log.Printf("Pipeline %d failed", pipelineID)
		}
	}
}

// findOrCreateProject finds an existing project or creates a new one
func (s *Server) findOrCreateProject(repo Repository) (*models.Project, error) {
	// Try to find existing project by repo URL
	projects, err := s.db.GetAllProjects()
	if err != nil {
		return nil, err
	}

	for _, p := range projects {
		if p.RepoURL == repo.CloneURL {
			return &p, nil
		}
	}

	// Create new project
	newProject := &models.NewProject{
		Name:        repo.FullName,
		RepoURL:     repo.CloneURL,
		AccessToken: "", // Empty for public repos, user can update later
	}

	return s.db.CreateProject(newProject)
}

// findCIConfig looks for CI configuration files in the workspace
func (s *Server) findCIConfig(workspaceDir string) string {
	// List of possible CI config file names
	configFiles := []string{
		".gitlab-ci.yml",
		".gitlab-ci.yaml",
		"gitlab-ci.yml",
		"gitlab-ci.yaml",
		".ci.yml",
		".ci.yaml",
	}

	for _, file := range configFiles {
		path := filepath.Join(workspaceDir, file)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	return ""
}

// executePipeline runs all jobs in the pipeline
func (s *Server) executePipeline(config *parser.PipelineConfig, workspaceDir string, pipelineID int) bool {
	pipelineSuccess := true

	for _, stageName := range config.Stages {
		log.Printf("Running stage: %s", stageName)

		for jobName, job := range config.Jobs {
			if job.Stage != stageName {
				continue
			}

			log.Printf("Running job: %s (image: %s)", jobName, job.Image)

			// Create job record in database
			var jobID int
			if s.db != nil && pipelineID > 0 {
				dbJob, err := s.db.CreateJob(pipelineID, jobName, job.Stage, job.Image)
				if err != nil {
					log.Printf("Failed to create job record: %v", err)
				} else {
					jobID = dbJob.ID
					s.db.UpdateJobStatus(jobID, "running", nil)
				}
			}

			// Handle different job types
			if job.Type == "docker-deploy" {
				// === Docker Deploy Job ===
				log.Printf("Executing Docker Deploy for %s", jobName)

				// Pull image first
				if err := s.docker.PullImage(job.Image); err != nil {
					log.Printf("Failed to pull image %s: %v", job.Image, err)
					if s.db != nil && jobID > 0 {
						exitCode := 1
						s.db.UpdateJobStatus(jobID, "failed", &exitCode)
					}
					pipelineSuccess = false
					continue
				}

				containerName := job.Properties["container_name"]
				portMapping := job.Properties["port"]

				err := s.docker.DeploySingleContainer(job.Image, containerName, portMapping)

				exitCode := 0
				status := "success"
				if err != nil {
					log.Printf("Deploy failed: %v", err)
					exitCode = 1
					status = "failed"
					pipelineSuccess = false
				}

				if s.db != nil && jobID > 0 {
					s.db.UpdateJobStatus(jobID, status, &exitCode)
				}

				if !pipelineSuccess {
					return false
				}

			} else if job.Type == "docker-compose-deploy" {
				// === Docker Compose Deploy Job ===
				log.Printf("Executing Docker Compose Deploy for %s", jobName)

				composeFile := job.Properties["file"]
				if composeFile == "" {
					composeFile = "docker-compose.yml"
				}
				serviceName := job.Properties["service"]

				err := s.docker.DeployCompose(workspaceDir, composeFile, serviceName)

				exitCode := 0
				status := "success"
				if err != nil {
					log.Printf("Compose Deploy failed: %v", err)
					exitCode = 1
					status = "failed"
					pipelineSuccess = false
				}

				if s.db != nil && jobID > 0 {
					s.db.UpdateJobStatus(jobID, status, &exitCode)
				}

				if !pipelineSuccess {
					return false
				}

			} else {
				// === Standard Shell Job ===

				// Pull the image
				log.Printf("Pulling image: %s", job.Image)
				if err := s.docker.PullImage(job.Image); err != nil {
					log.Printf("Failed to pull image %s: %v", job.Image, err)
					if s.db != nil && jobID > 0 {
						exitCode := 1
						s.db.UpdateJobStatus(jobID, "failed", &exitCode)
					}
					pipelineSuccess = false
					continue
				}

				// Define environment variables
				envVars := []string{
					fmt.Sprintf("CI_PIPELINE_ID=%d", pipelineID),
					fmt.Sprintf("CI_JOB_ID=%d", jobID),
					"CI_PROJECT_DIR=/workspace",
				}

				// Run the job with workspace mounted
				containerID, err := s.docker.RunJobWithVolume(job.Image, job.Script, workspaceDir, envVars)
				if err != nil {
					log.Printf("Failed to start job %s: %v", jobName, err)
					if s.db != nil && jobID > 0 {
						exitCode := 1
						s.db.UpdateJobStatus(jobID, "failed", &exitCode)
					}
					pipelineSuccess = false
					continue
				}

				// Collect and store logs
				s.collectLogs(containerID, jobID)

				// Wait for container to finish
				statusCode, err := s.docker.WaitForContainer(containerID)
				if err != nil {
					log.Printf("Error waiting for container: %v", err)
				}

				// Update job status
				exitCode := int(statusCode)
				if s.db != nil && jobID > 0 {
					status := "success"
					if statusCode != 0 {
						status = "failed"
					}
					s.db.UpdateJobStatus(jobID, status, &exitCode)
				}

				if statusCode != 0 {
					log.Printf("Job %s failed with exit code %d", jobName, statusCode)
					pipelineSuccess = false
					// Stop pipeline on first failure
					return false
				}
			}

			log.Printf("Job %s completed successfully", jobName)
		}
	}

	return pipelineSuccess
}

// collectLogs collects logs from the container and stores them in the database
func (s *Server) collectLogs(containerID string, jobID int) {
	reader, err := s.docker.GetLogs(containerID)
	if err != nil {
		log.Printf("Failed to get logs: %v", err)
		return
	}
	defer reader.Close()

	scanner := bufio.NewScanner(reader)
	var logBatch []string

	for scanner.Scan() {
		line := scanner.Text()

		// Docker log stream has 8-byte header, try to clean it
		cleanLine := line
		if len(line) > 8 {
			cleanLine = strings.TrimRight(line[8:], "\x00")
		}

		if cleanLine == "" {
			continue
		}

		// Print to console
		fmt.Println(cleanLine)

		// Add to batch
		logBatch = append(logBatch, cleanLine)

		// Store in batches of 10
		if len(logBatch) >= 10 && s.db != nil && jobID > 0 {
			if err := s.db.CreateLogBatch(jobID, logBatch); err != nil {
				log.Printf("Failed to store logs: %v", err)
			}
			logBatch = nil
		}
	}

	// Store remaining logs
	if len(logBatch) > 0 && s.db != nil && jobID > 0 {
		if err := s.db.CreateLogBatch(jobID, logBatch); err != nil {
			log.Printf("Failed to store remaining logs: %v", err)
		}
	}
}

// cloneRepo clones a repository (wrapper for git.Clone)
// commitHash is optional - pass empty string to get the latest commit on the branch
func (s *Server) cloneRepo(repoURL, branch, destPath, token, commitHash string) error {
	return git.Clone(repoURL, branch, destPath, token, commitHash)
}

// cleanupWorkspace removes the workspace directory (wrapper for git.Cleanup)
func (s *Server) cleanupWorkspace(path string) error {
	return git.Cleanup(path)
}

// parseConfig parses a CI configuration file
func (s *Server) parseConfig(configPath string) (*parser.PipelineConfig, error) {
	p := parser.NewParser(configPath)
	return p.Parse()
}