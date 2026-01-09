package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/database"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/docker"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/executor"

	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/pkg/logger"
)

// Server represents the API server
type Server struct {
	db                 *database.DB
	docker             *docker.DockerExecutor
	port               string
	pipelineExecutor   *executor.PipelineExecutor
	deploymentExecutor *executor.DeploymentExecutor
}

// NewServer creates a new API server
func NewServer(db *database.DB, port string) (*Server, error) {
	docker, err := docker.NewDockerExecutor()
	if err != nil {
		return nil, fmt.Errorf("failed to create docker executor: %w", err)
	}

	pipelineExecutor := executor.NewPipelineExecutor(db, docker)
	deploymentExecutor := executor.NewDeploymentExecutor(db, docker)

	return &Server{
		db:                 db,
		docker:             docker,
		port:               port,
		pipelineExecutor:   pipelineExecutor,
		deploymentExecutor: deploymentExecutor,
	}, nil
}

// enableCORS adds CORS headers to the response
func enableCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-GitHub-Event")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Start starts the API server
func (s *Server) Start() error {
	InitializeOAuth()

	// Health check
	http.HandleFunc("/health", s.handleHealth)

	// Webhook
	http.HandleFunc("/webhook/github", s.handleGitHubWebhook)

	// Auth routes
	http.HandleFunc("/auth/google/login", s.handleAuthLogin)
	http.HandleFunc("/auth/google/callback", s.handleAuthCallback)
	http.HandleFunc("/auth/github/login", s.handleAuthLogin)
	http.HandleFunc("/auth/github/callback", s.handleAuthCallback)

	// API v1 routes
	http.HandleFunc("/api/v1/projects", s.AuthMiddleware(s.handleProjects))
	http.HandleFunc("/api/v1/projects/", s.AuthMiddleware(s.routeProjectsSubpath))

	logger.Info("Starting API server on port " + s.port)
	logger.Info("Endpoints:")
	logger.Info("  - GET    /health")
	logger.Info("  - POST   /webhook/github")
	logger.Info("  - GET    /auth/{provider}/login")
	logger.Info("  - GET    /auth/{provider}/callback")
	logger.Info("  - GET    /api/v1/projects")
	logger.Info("  - POST   /api/v1/projects")
	logger.Info("  - GET    /api/v1/projects/{id}")
	logger.Info("  - PUT    /api/v1/projects/{id}")
	logger.Info("  - DELETE /api/v1/projects/{id}")
	logger.Info("  - GET    /api/v1/projects/{id}/members")
	logger.Info("  - POST   /api/v1/projects/{id}/members")
	logger.Info("  - DELETE /api/v1/projects/{id}/members/{userId}")
	logger.Info("  - GET    /api/v1/projects/{id}/variables")
	logger.Info("  - POST   /api/v1/projects/{id}/variables")
	logger.Info("  - DELETE /api/v1/projects/{id}/variables/{key}")
	logger.Info("  - GET    /api/v1/projects/{id}/pipelines")
	logger.Info("  - POST   /api/v1/projects/{id}/pipelines")
	logger.Info("  - GET    /api/v1/projects/{id}/pipelines/{id}")
	logger.Info("  - GET    /api/v1/projects/{id}/pipelines/{id}/jobs")
	logger.Info("  - GET    /api/v1/projects/{id}/pipelines/{id}/jobs/{id}")
	logger.Info("  - GET    /api/v1/projects/{id}/pipelines/{id}/jobs/{id}/logs")

	return http.ListenAndServe(":"+s.port, enableCORS(http.DefaultServeMux))
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

	// /api/v1/projects/{projectId}/members
	if len(parts) == 2 && parts[1] == "members" {
		s.handleProjectMembers(w, r)
		return
	}

	// /api/v1/projects/{projectId}/members/{userId}
	if len(parts) == 3 && parts[1] == "members" {
		s.handleProjectMember(w, r)
		return
	}

	// /api/v1/projects/{projectId}/variables
	if len(parts) == 2 && parts[1] == "variables" {
		s.handleVariables(w, r)
		return
	}

	// /api/v1/projects/{projectId}/variables/{key}
	if len(parts) == 3 && parts[1] == "variables" {
		s.handleVariable(w, r)
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

	// /api/v1/projects/{projectId}/pipelines/{pipelineId}/deployment
	if len(parts) == 4 && parts[1] == "pipelines" && parts[3] == "deployment" {
		s.handleDeployment(w, r)
		return
	}

	// /api/v1/projects/{projectId}/pipelines/{pipelineId}/deployment/logs
	if len(parts) == 5 && parts[1] == "pipelines" && parts[3] == "deployment" && parts[4] == "logs" {
		s.handleDeploymentLogs(w, r)
		return
	}

	respondError(w, http.StatusNotFound, "Not found")
}
