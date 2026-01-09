package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/git"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/models"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/pkg/logger"
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
func (s *Server) handleVariables(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDFromPath(r.URL.Path, 3)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.listVariables(w, r, projectID)
	case http.MethodPost:
		s.createVariable(w, r, projectID)
	default:
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *Server) listVariables(w http.ResponseWriter, r *http.Request, projectID int) {
	_, err := getUserIDFromContext(r)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	variables, err := s.db.GetVariablesByProject(projectID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to get variables")
		return
	}

	for i := range variables {
		if variables[i].IsSecret {
			variables[i].Value = "*****"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(variables)
}

func (s *Server) createVariable(w http.ResponseWriter, r *http.Request, projectID int) {
	_, err := getUserIDFromContext(r)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var v models.Variable
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	v.ProjectID = projectID
	if err := s.db.CreateVariable(&v); err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create variable: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) handleVariable(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDFromPath(r.URL.Path, 3)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 6 {
		respondError(w, http.StatusBadRequest, "Invalid path")
		return
	}
	key := parts[5]

	if r.Method == http.MethodDelete {
		s.deleteVariable(w, r, projectID, key)
	} else {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *Server) deleteVariable(w http.ResponseWriter, r *http.Request, projectID int, key string) {
	_, err := getUserIDFromContext(r)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	if err := s.db.DeleteVariable(projectID, key); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to delete variable")
		return
	}

	w.WriteHeader(http.StatusOK)
}

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

	userID, err := getUserIDFromContext(r)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	projects, err := s.db.GetProjectsForUser(userID)
	if err != nil {
		logger.Error("Failed to get projects: " + err.Error())
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

	userID, err := getUserIDFromContext(r)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	newProject.OwnerID = userID

	project, err := s.db.CreateProject(&newProject)
	if err != nil {
		logger.Error("Failed to create project: " + err.Error())
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

	userID, err := getUserIDFromContext(r)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	project, err := s.db.GetProject(projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}

	// Check access permissions (Owner or Member)
	if project.OwnerID != userID {
		members, err := s.db.GetProjectMembers(projectID)
		if err != nil {
			logger.Error("Failed to check membership: " + err.Error())
			respondError(w, http.StatusInternalServerError, "Failed to check permissions")
			return
		}

		isMember := false
		for _, m := range members {
			if m.UserID == userID {
				isMember = true
				break
			}
		}

		if !isMember {
			respondError(w, http.StatusForbidden, "You do not have access to this project")
			return
		}
	}

	respondJSON(w, http.StatusOK, project)
}

// updateProject updates an existing project
func (s *Server) updateProject(w http.ResponseWriter, r *http.Request, projectID int) {
	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "Database not available")
		return
	}

	userID, err := getUserIDFromContext(r)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	existingProject, err := s.db.GetProject(projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}

	if existingProject.OwnerID != userID {
		respondError(w, http.StatusForbidden, "You are not the owner of this project")
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
		logger.Error("Failed to update project: " + err.Error())
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

	userID, err := getUserIDFromContext(r)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	existingProject, err := s.db.GetProject(projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}

	if existingProject.OwnerID != userID {
		respondError(w, http.StatusForbidden, "You are not the owner of this project")
		return
	}

	if err := s.db.DeleteProject(projectID); err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// === Project Members Handlers ===

// handleProjectMembers handles /api/v1/projects/{projectId}/members
func (s *Server) handleProjectMembers(w http.ResponseWriter, r *http.Request) {
	// Extract project ID
	projectID, err := parseIDFromPath(r.URL.Path, 3)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.listProjectMembers(w, r, projectID)
	case http.MethodPost:
		s.inviteMember(w, r, projectID)
	default:
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleProjectMember handles /api/v1/projects/{projectId}/members/{userId}
func (s *Server) handleProjectMember(w http.ResponseWriter, r *http.Request) {
	// Extract project ID
	projectID, err := parseIDFromPath(r.URL.Path, 3)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	userID, err := parseIDFromPath(r.URL.Path, 5)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	switch r.Method {
	case http.MethodDelete:
		s.removeProjectMember(w, r, projectID, userID)
	default:
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// listProjectMembers returns all members of a project
func (s *Server) listProjectMembers(w http.ResponseWriter, r *http.Request, projectID int) {
	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "Database not available")
		return
	}

	members, err := s.db.GetProjectMembers(projectID)
	if err != nil {
		logger.Error("Failed to get project members: " + err.Error())
		respondError(w, http.StatusInternalServerError, "Failed to get project members")
		return
	}

	respondJSON(w, http.StatusOK, members)
}

// inviteMember adds a user to a project by email
func (s *Server) inviteMember(w http.ResponseWriter, r *http.Request, projectID int) {
	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "Database not available")
		return
	}

	userID, err := getUserIDFromContext(r)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	project, err := s.db.GetProject(projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}

	if project.OwnerID != userID {
		respondError(w, http.StatusForbidden, "Only the owner can invite members")
		return
	}

	var reqBody struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if reqBody.Email == "" {
		respondError(w, http.StatusBadRequest, "Email is required")
		return
	}
	if reqBody.Role == "" {
		reqBody.Role = "viewer"
	}

	userToInvite, err := s.db.GetUserByEmail(reqBody.Email)
	if err != nil {
		respondError(w, http.StatusNotFound, "User not found. They must sign in first.")
		return
	}

	if err := s.db.AddProjectMember(projectID, userToInvite.ID, reqBody.Role); err != nil {
		logger.Error("Failed to add member: " + err.Error())
		respondError(w, http.StatusInternalServerError, "Failed to add member")
		return
	}

	respondJSON(w, http.StatusCreated, map[string]string{"message": "Member added"})
}

// removeProjectMember removes a member
func (s *Server) removeProjectMember(w http.ResponseWriter, r *http.Request, projectID, targetUserID int) {
	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "Database not available")
		return
	}

	userID, err := getUserIDFromContext(r)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	project, err := s.db.GetProject(projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}

	if project.OwnerID != userID {
		respondError(w, http.StatusForbidden, "Only the owner can remove members")
		return
	}

	if err := s.db.RemoveProjectMember(projectID, targetUserID); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to remove member")
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
		logger.Error("Failed to get pipelines: " + err.Error())
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

	// Get latest commit hash
	commitHash, err := git.GetRemoteHeadHash(project.RepoURL, reqBody.Branch, project.AccessToken)
	if err != nil {
		logger.Error("Failed to get latest commit hash: " + err.Error())
		respondError(w, http.StatusInternalServerError, "Failed to get latest commit hash")
		return
	}

	// Create pipeline record
	pipeline, err := s.db.CreatePipeline(projectID, reqBody.Branch, commitHash)
	if err != nil {
		logger.Error("Failed to create pipeline: " + err.Error())
		respondError(w, http.StatusInternalServerError, "Failed to create pipeline")
		return
	}

	// Trigger pipeline execution asynchronously
	go s.runPipelineFromManualTrigger(project, pipeline, reqBody.Branch)

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
		logger.Error("Failed to get jobs: " + err.Error())
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
		logger.Error("Failed to get logs: " + err.Error())
		respondError(w, http.StatusInternalServerError, "Failed to get logs")
		return
	}

	respondJSON(w, http.StatusOK, logs)
}

// === Deployment Handlers ===

// handleDeployment retrieves the deployment for a pipeline
func (s *Server) handleDeployment(w http.ResponseWriter, r *http.Request) {
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

	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "Database not available")
		return
	}

	// Verify project exists
	_, err = s.db.GetProject(projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}

	deployment, err := s.db.GetDeploymentByPipeline(pipelineID)
	if err != nil {
		log.Printf("Failed to get deployment: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to get deployment")
		return
	}

	if deployment == nil {
		respondError(w, http.StatusNotFound, "Deployment not found")
		return
	}

	respondJSON(w, http.StatusOK, deployment)
}

// handleDeploymentLogs retrieves logs for a deployment
func (s *Server) handleDeploymentLogs(w http.ResponseWriter, r *http.Request) {
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

	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "Database not available")
		return
	}

	// Verify project exists
	_, err = s.db.GetProject(projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}

	logs, err := s.db.GetDeploymentLogs(pipelineID)
	if err != nil {
		log.Printf("Failed to get deployment logs: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to get deployment logs")
		return
	}

	respondJSON(w, http.StatusOK, logs)
}

// === System Handlers ===

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
		logger.Info("Ignoring non-push event: " + eventType)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"message": "event ignored"})
		return
	}

	// Parse the push event
	var pushEvent models.PushEvent
	if err := json.NewDecoder(r.Body).Decode(&pushEvent); err != nil {
		logger.Error("Failed to parse webhook payload: " + err.Error())
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	// Ignore branch deletions
	if pushEvent.Deleted {
		logger.Info("Ignoring branch deletion event")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"message": "deletion ignored"})
		return
	}

	// Extract branch name from ref (refs/heads/main -> main)
	branch := strings.TrimPrefix(pushEvent.Ref, "refs/heads/")
	commitHash := pushEvent.After

	logger.Info("Received push event for %s on branch %s (commit: %s)",
		pushEvent.Repository.FullName, branch, commitHash[:8])

	// Run pipeline asynchronously
	go s.runPipelineFromWebhook(pushEvent, branch, commitHash)

	// Respond immediately
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Pipeline triggered",
		"branch":  branch,
		"commit":  commitHash,
	})
}
