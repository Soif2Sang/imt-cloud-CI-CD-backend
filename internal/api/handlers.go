package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/models"
)

// === Helper Functions ===

// respondJSON sends a JSON response
func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}

// respondError sends an error response
func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

// parseIDFromPath extracts an ID from a URL path segment
func parseIDFromPath(path string, segment int) (int, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if segment >= len(parts) {
		return 0, strconv.ErrSyntax
	}
	return strconv.Atoi(parts[segment])
}

// sanitizeProjectName sanitizes the project name for Docker Compose
func sanitizeProjectName(name string) string {
	name = strings.ToLower(name)
	reg := regexp.MustCompile("[^a-z0-9]+")
	name = reg.ReplaceAllString(name, "-")
	return strings.Trim(name, "-")
}

// === Projects Handlers ===

// handleProjects handles /api/v1/projects
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listProjects(w, r)
	case http.MethodPost:
		s.createProject(w, r)
	default:
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleProject handles /api/v1/projects/{projectId}
func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	// Extract project ID from path: /api/v1/projects/{projectId}
	projectID, err := parseIDFromPath(r.URL.Path, 3)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getProject(w, r, projectID)
	case http.MethodPut:
		s.updateProject(w, r, projectID)
	case http.MethodDelete:
		s.deleteProject(w, r, projectID)
	default:
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// listProjects returns all projects
func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "Database not available")
		return
	}

	projects, err := s.db.GetAllProjects()
	if err != nil {
		log.Printf("Failed to get projects: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to get projects")
		return
	}

	respondJSON(w, http.StatusOK, projects)
}

// createProject creates a new project
func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "Database not available")
		return
	}

	var newProject models.NewProject
	if err := json.NewDecoder(r.Body).Decode(&newProject); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if newProject.Name == "" || newProject.RepoURL == "" {
		respondError(w, http.StatusBadRequest, "Name and repo_url are required")
		return
	}

	project, err := s.db.CreateProject(&newProject)
	if err != nil {
		log.Printf("Failed to create project: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to create project")
		return
	}

	respondJSON(w, http.StatusCreated, project)
}

// getProject returns a project by ID
func (s *Server) getProject(w http.ResponseWriter, r *http.Request, projectID int) {
	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "Database not available")
		return
	}

	project, err := s.db.GetProject(projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}

	respondJSON(w, http.StatusOK, project)
}

// updateProject updates an existing project
func (s *Server) updateProject(w http.ResponseWriter, r *http.Request, projectID int) {
	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "Database not available")
		return
	}

	var updateData models.NewProject
	if err := json.NewDecoder(r.Body).Decode(&updateData); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if updateData.Name == "" || updateData.RepoURL == "" {
		respondError(w, http.StatusBadRequest, "Name and repo_url are required")
		return
	}

	project, err := s.db.UpdateProject(projectID, &updateData)
	if err != nil {
		log.Printf("Failed to update project: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to update project")
		return
	}

	respondJSON(w, http.StatusOK, project)
}

// deleteProject deletes a project
func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request, projectID int) {
	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "Database not available")
		return
	}

	if err := s.db.DeleteProject(projectID); err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// === Pipelines Handlers ===

// handlePipelines handles /api/v1/projects/{projectId}/pipelines
func (s *Server) handlePipelines(w http.ResponseWriter, r *http.Request) {
	// Extract project ID from path: /api/v1/projects/{projectId}/pipelines
	projectID, err := parseIDFromPath(r.URL.Path, 3)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.listPipelines(w, r, projectID)
	case http.MethodPost:
		s.triggerPipeline(w, r, projectID)
	default:
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handlePipeline handles /api/v1/projects/{projectId}/pipelines/{pipelineId}
func (s *Server) handlePipeline(w http.ResponseWriter, r *http.Request) {
	// Extract IDs from path: /api/v1/projects/{projectId}/pipelines/{pipelineId}
	projectID, err := parseIDFromPath(r.URL.Path, 3)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	pipelineID, err := parseIDFromPath(r.URL.Path, 5)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid pipeline ID")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getPipeline(w, r, projectID, pipelineID)
	default:
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// listPipelines returns all pipelines for a project
func (s *Server) listPipelines(w http.ResponseWriter, r *http.Request, projectID int) {
	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "Database not available")
		return
	}

	// Verify project exists
	_, err := s.db.GetProject(projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}

	pipelines, err := s.db.GetPipelinesByProject(projectID)
	if err != nil {
		log.Printf("Failed to get pipelines: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to get pipelines")
		return
	}

	respondJSON(w, http.StatusOK, pipelines)
}

// triggerPipeline triggers a new pipeline for a project
func (s *Server) triggerPipeline(w http.ResponseWriter, r *http.Request, projectID int) {
	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "Database not available")
		return
	}

	// Get project
	project, err := s.db.GetProject(projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}

	// Parse request body
	var reqBody struct {
		Branch string `json:"branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		reqBody.Branch = "main" // Default branch
	}
	if reqBody.Branch == "" {
		reqBody.Branch = "main"
	}

	// Create pipeline record
	pipeline, err := s.db.CreatePipeline(projectID, reqBody.Branch, "manual-trigger")
	if err != nil {
		log.Printf("Failed to create pipeline: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to create pipeline")
		return
	}

	// Trigger pipeline execution asynchronously
	go s.runPipelineForProject(project, pipeline, reqBody.Branch)

	respondJSON(w, http.StatusCreated, pipeline)
}

// getPipeline returns a specific pipeline
func (s *Server) getPipeline(w http.ResponseWriter, r *http.Request, projectID, pipelineID int) {
	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "Database not available")
		return
	}

	// Verify project exists
	_, err := s.db.GetProject(projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}

	pipeline, err := s.db.GetPipeline(pipelineID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Pipeline not found")
		return
	}

	// Verify pipeline belongs to project
	if pipeline.ProjectID != projectID {
		respondError(w, http.StatusNotFound, "Pipeline not found")
		return
	}

	respondJSON(w, http.StatusOK, pipeline)
}

// === Jobs Handlers ===

// handleJobs handles /api/v1/projects/{projectId}/pipelines/{pipelineId}/jobs
func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	// Extract IDs from path
	projectID, err := parseIDFromPath(r.URL.Path, 3)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	pipelineID, err := parseIDFromPath(r.URL.Path, 5)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid pipeline ID")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.listJobs(w, r, projectID, pipelineID)
	default:
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleJob handles /api/v1/projects/{projectId}/pipelines/{pipelineId}/jobs/{jobId}
func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	// Extract IDs from path
	projectID, err := parseIDFromPath(r.URL.Path, 3)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	pipelineID, err := parseIDFromPath(r.URL.Path, 5)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid pipeline ID")
		return
	}

	jobID, err := parseIDFromPath(r.URL.Path, 7)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid job ID")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getJob(w, r, projectID, pipelineID, jobID)
	default:
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// listJobs returns all jobs for a pipeline
func (s *Server) listJobs(w http.ResponseWriter, r *http.Request, projectID, pipelineID int) {
	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "Database not available")
		return
	}

	// Verify project exists
	_, err := s.db.GetProject(projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}

	// Verify pipeline exists and belongs to project
	pipeline, err := s.db.GetPipeline(pipelineID)
	if err != nil || pipeline.ProjectID != projectID {
		respondError(w, http.StatusNotFound, "Pipeline not found")
		return
	}

	jobs, err := s.db.GetJobsByPipeline(pipelineID)
	if err != nil {
		log.Printf("Failed to get jobs: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to get jobs")
		return
	}

	respondJSON(w, http.StatusOK, jobs)
}

// getJob returns a specific job
func (s *Server) getJob(w http.ResponseWriter, r *http.Request, projectID, pipelineID, jobID int) {
	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "Database not available")
		return
	}

	// Verify project exists
	_, err := s.db.GetProject(projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}

	// Verify pipeline exists and belongs to project
	pipeline, err := s.db.GetPipeline(pipelineID)
	if err != nil || pipeline.ProjectID != projectID {
		respondError(w, http.StatusNotFound, "Pipeline not found")
		return
	}

	job, err := s.db.GetJob(jobID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Job not found")
		return
	}

	// Verify job belongs to pipeline
	if job.PipelineID != pipelineID {
		respondError(w, http.StatusNotFound, "Job not found")
		return
	}

	respondJSON(w, http.StatusOK, job)
}

// === Logs Handlers ===

// handleLogs handles /api/v1/projects/{projectId}/pipelines/{pipelineId}/jobs/{jobId}/logs
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	// Extract IDs from path
	projectID, err := parseIDFromPath(r.URL.Path, 3)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	pipelineID, err := parseIDFromPath(r.URL.Path, 5)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid pipeline ID")
		return
	}

	jobID, err := parseIDFromPath(r.URL.Path, 7)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid job ID")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getJobLogs(w, r, projectID, pipelineID, jobID)
	default:
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// getJobLogs returns logs for a specific job
func (s *Server) getJobLogs(w http.ResponseWriter, r *http.Request, projectID, pipelineID, jobID int) {
	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "Database not available")
		return
	}

	// Verify project exists
	_, err := s.db.GetProject(projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}

	// Verify pipeline exists and belongs to project
	pipeline, err := s.db.GetPipeline(pipelineID)
	if err != nil || pipeline.ProjectID != projectID {
		respondError(w, http.StatusNotFound, "Pipeline not found")
		return
	}

	// Verify job exists and belongs to pipeline
	job, err := s.db.GetJob(jobID)
	if err != nil || job.PipelineID != pipelineID {
		respondError(w, http.StatusNotFound, "Job not found")
		return
	}

	logs, err := s.db.GetLogsByJob(jobID)
	if err != nil {
		log.Printf("Failed to get logs: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to get logs")
		return
	}

	respondJSON(w, http.StatusOK, logs)
}

// === Pipeline Execution for Manual Trigger ===

// runPipelineForProject runs a pipeline for a project (used by manual trigger)
func (s *Server) runPipelineForProject(project *models.Project, pipeline *models.Pipeline, branch string) {
	log.Printf("Starting manual pipeline %d for project %s", pipeline.ID, project.Name)

	// Update status to running
	s.db.UpdatePipelineStatus(pipeline.ID, "running")

	// Create workspace
	workspaceDir := "/tmp/cicd-workspaces/" + project.Name + "-" + strconv.Itoa(pipeline.ID)

	// Clone repository
	if err := s.cloneAndRunPipeline(project, pipeline, branch, workspaceDir); err != nil {
		log.Printf("Pipeline %d failed: %v", pipeline.ID, err)
		s.db.UpdatePipelineStatus(pipeline.ID, "failed")
		return
	}
}

// cloneAndRunPipeline clones the repo and runs the pipeline
func (s *Server) cloneAndRunPipeline(project *models.Project, pipeline *models.Pipeline, branch, workspaceDir string) error {
	// Import git package functions
	// Clone repository at specific commit if available
	if err := s.cloneRepo(project.RepoURL, branch, workspaceDir, project.AccessToken, pipeline.CommitHash); err != nil {
		return err
	}
	defer s.cleanupWorkspace(workspaceDir)

	pipelineFilename := project.PipelineFilename
	if pipelineFilename == "" {
		pipelineFilename = ".gitlab-ci.yml"
	}
	deploymentFilename := project.DeploymentFilename
	if deploymentFilename == "" {
		deploymentFilename = "docker-compose.yml"
	}

	// Find CI config
	configPath := filepath.Join(workspaceDir, pipelineFilename)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Printf("CI config file not found at %s", configPath)
		return nil
	}

	// Parse and execute
	config, err := s.parseConfig(configPath)
	if err != nil {
		return err
	}

	success := s.executePipeline(config, workspaceDir, pipeline.ID)

	// Deploy if successful
	if success {
		log.Printf("Pipeline successful. Starting deployment using %s...", deploymentFilename)
		sanitizedProjectName := sanitizeProjectName(project.Name)
		if err := s.docker.DeployCompose(workspaceDir, deploymentFilename, sanitizedProjectName, ""); err != nil {
			log.Printf("Deployment failed: %v", err)
			s.db.UpdatePipelineStatus(pipeline.ID, "failed")
		} else {
			log.Printf("Deployment successful!")
			s.db.UpdatePipelineStatus(pipeline.ID, "success")
		}
	} else {
		s.db.UpdatePipelineStatus(pipeline.ID, "failed")
	}

	return nil
}