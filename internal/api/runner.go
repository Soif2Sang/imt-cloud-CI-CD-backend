package api

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/git"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/models"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/parser/pipeline"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/pkg/logger"
)

// runPipelineLogic executes the CI/CD pipeline logic
// This unifies logic from webhook and manual trigger
func (s *Server) runPipelineLogic(params models.PipelineRunParams) {
	// Fetch project details for SSH/Registry info
	var project *models.Project
	if s.db != nil {
		project, _ = s.db.GetProject(params.ProjectID)
	}

	// Create a unique workspace directory
	workspaceDir := filepath.Join("/tmp", "cicd-workspaces", fmt.Sprintf("%s-%s-%d", params.RepoName, params.CommitHash[:8], time.Now().Unix()))

	logger.Info(fmt.Sprintf("Starting pipeline for %s", params.RepoName))

	// Clone the repository
	logger.Info(fmt.Sprintf("Cloning repository to %s", workspaceDir))

	if err := git.Clone(params.RepoURL, params.Branch, workspaceDir, params.AccessToken, params.CommitHash); err != nil {
		logger.Error("Failed to clone repository: " + err.Error())
		if s.db != nil && params.PipelineID > 0 {
			s.db.UpdatePipelineStatus(params.PipelineID, "failed")
		}
		return
	}
	defer git.Cleanup(workspaceDir)

	// Find and parse the CI config file
	configPath := filepath.Join(workspaceDir, params.PipelineFilename)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		logger.Warn(fmt.Sprintf("CI config file not found at %s", configPath))
		if s.db != nil && params.PipelineID > 0 {
			s.db.UpdatePipelineStatus(params.PipelineID, "failed")
		}
		return
	}

	logger.Info(fmt.Sprintf("Found CI config: %s", configPath))

	// Parse the CI config
	p := pipeline.NewParser(configPath)
	config, err := p.Parse()
	if err != nil {
		logger.Error("Failed to parse CI config: " + err.Error())
		if s.db != nil && params.PipelineID > 0 {
			s.db.UpdatePipelineStatus(params.PipelineID, "failed")
		}
		return
	}

	logger.Info(fmt.Sprintf("Config loaded with %d stages", len(config.Stages)))

	// Pre-create jobs and deployment for visualization
	if s.db != nil && params.PipelineID > 0 {
		// Pre-create jobs
		for _, stageName := range config.Stages {
			for jobName, job := range config.Jobs {
				if job.Stage == stageName {
					if _, err := s.db.CreateJob(params.PipelineID, jobName, job.Stage, job.Image); err != nil {
						logger.Error(fmt.Sprintf("Failed to pre-create job %s: %v", jobName, err))
					}
				}
			}
		}
		// Pre-create deployment
		if _, err := s.db.CreatePendingDeployment(params.PipelineID); err != nil {
			logger.Error("Failed to pre-create deployment: " + err.Error())
		}
	}

	// Execute the pipeline jobs using delegated executor
	pipelineSuccess := s.pipelineExecutor.Execute(config, workspaceDir, params.PipelineID, project)

	// Deploy if successful
	if pipelineSuccess {
		logger.Info(fmt.Sprintf("Pipeline successful. Starting deployment using %s...", params.DeploymentFilename))

		var deploymentID int
		if s.db != nil && params.PipelineID > 0 {
			deploy, err := s.db.GetDeploymentByPipeline(params.PipelineID)
			if err != nil {
				// Fallback if not found
				deploy, err = s.db.CreateDeployment(params.PipelineID)
				if err != nil {
					logger.Error("Failed to create deployment record: " + err.Error())
				}
			}

			if deploy != nil {
				deploymentID = deploy.ID
				s.db.UpdateDeploymentStatus(deploymentID, "deploying")
			}
		}

		// Deploy to environment using delegated executor
		_, err := s.deploymentExecutor.Execute(project, params, workspaceDir)

		if err != nil {
			logger.Error("Deployment failed: " + err.Error())

			// Attempt Rollback
			rollbackSuccess := false
			if s.db != nil && project != nil {
				lastPipeline, _ := s.db.GetLastSuccessfulPipeline(project.ID)
				if lastPipeline != nil && lastPipeline.CommitHash != "" {
					logger.Info(fmt.Sprintf("Attempting rollback to commit %s", lastPipeline.CommitHash))

					// Prepare rollback params
					rollbackParams := params
					rollbackParams.CommitHash = lastPipeline.CommitHash
					// Note: We use the same config filenames as current project settings.

					// Create unique workspace for rollback
					rollbackDir := filepath.Join("/tmp", "cicd-workspaces", fmt.Sprintf("%s-rollback-%s-%d", params.RepoName, rollbackParams.CommitHash[:8], time.Now().Unix()))

					logger.Info(fmt.Sprintf("Cloning rollback commit to %s", rollbackDir))
					if cloneErr := git.Clone(rollbackParams.RepoURL, rollbackParams.Branch, rollbackDir, rollbackParams.AccessToken, rollbackParams.CommitHash); cloneErr == nil {
						defer git.Cleanup(rollbackDir)

						// Log rollback start
						s.db.CreateDeploymentLog(params.PipelineID, "=== ROLLBACK STARTED ===")

						// Run deployment for old version using delegated executor
						_, rbErr := s.deploymentExecutor.Execute(project, rollbackParams, rollbackDir)

						if rbErr == nil {
							rollbackSuccess = true
							logger.Info("Rollback successful")
						} else {
							logger.Error("Rollback failed: " + rbErr.Error())
						}
					} else {
						logger.Error("Rollback clone failed: " + cloneErr.Error())
					}
				}
			}

			pipelineSuccess = false
			if s.db != nil && deploymentID > 0 {
				if rollbackSuccess {
					s.db.UpdateDeploymentStatus(deploymentID, "rolled_back")
				} else {
					s.db.UpdateDeploymentStatus(deploymentID, "failed")
				}
			}
		} else {
			logger.Info("Deployment successful!")
			if s.db != nil && deploymentID > 0 {
				s.db.UpdateDeploymentStatus(deploymentID, "success")
			}
		}
	}

	// Update final pipeline status
	if s.db != nil && params.PipelineID > 0 {
		if pipelineSuccess {
			s.db.UpdatePipelineStatus(params.PipelineID, "success")
			logger.Info(fmt.Sprintf("Pipeline %d completed successfully", params.PipelineID))
		} else {
			s.db.UpdatePipelineStatus(params.PipelineID, "failed")
			logger.Error(fmt.Sprintf("Pipeline %d failed", params.PipelineID))

			// Mark pending deployment as failed if pipeline failed
			deploy, err := s.db.GetDeploymentByPipeline(params.PipelineID)
			if err != nil && deploy != nil {
				s.db.UpdateDeploymentStatus(deploy.ID, "failed")
			}
		}
	}
}

// === Higher level Wrappers ===

// runPipelineFromWebhook adapts webhook data to the unified runner
func (s *Server) runPipelineFromWebhook(pushEvent models.PushEvent, branch, commitHash string) {
	// Find or create project in database
	var projectID int
	var accessToken string
	var pipelineFilename string
	var deploymentFilename string

	if s.db != nil {
		project, err := s.db.FindProjectByUrl(pushEvent.Repository.CloneURL)
		if err != nil {
			logger.Error(fmt.Sprintf("Project not found for repo %s: %v. Ignoring webhook.", pushEvent.Repository.CloneURL, err))
			return
		}

		projectID = project.ID
		accessToken = project.AccessToken
		pipelineFilename = project.PipelineFilename
		deploymentFilename = project.DeploymentFilename
	}

	if pipelineFilename == "" {
		pipelineFilename = ".gitlab-ci.yml"
	}
	if deploymentFilename == "" {
		deploymentFilename = "docker-compose.yml"
	}

	// Create pipeline record
	var pipelineID int
	if s.db != nil && projectID > 0 {
		pipeline, err := s.db.CreatePipeline(projectID, branch, commitHash)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to create pipeline record: %v", err))
		} else {
			pipelineID = pipeline.ID
			logger.Info(fmt.Sprintf("Pipeline created with ID: %d", pipelineID))
			s.db.UpdatePipelineStatus(pipelineID, "running")
		}
	}

	params := models.PipelineRunParams{
		RepoURL:            pushEvent.Repository.CloneURL,
		RepoName:           pushEvent.Repository.Name,
		Branch:             branch,
		CommitHash:         commitHash,
		AccessToken:        accessToken,
		PipelineFilename:   pipelineFilename,
		DeploymentFilename: deploymentFilename,
		ProjectID:          projectID,
		PipelineID:         pipelineID,
	}

	s.runPipelineLogic(params)
}

// runPipelineFromManualTrigger adapts manual trigger data to the unified runner
func (s *Server) runPipelineFromManualTrigger(project *models.Project, pipeline *models.Pipeline, branch string) {
	logger.Info(fmt.Sprintf("Starting manual pipeline %d for project %s", pipeline.ID, project.Name))

	// Update status to running
	s.db.UpdatePipelineStatus(pipeline.ID, "running")

	pipelineFilename := project.PipelineFilename
	if pipelineFilename == "" {
		pipelineFilename = ".gitlab-ci.yml"
	}
	deploymentFilename := project.DeploymentFilename
	if deploymentFilename == "" {
		deploymentFilename = "docker-compose.yml"
	}

	params := models.PipelineRunParams{
		RepoURL:            project.RepoURL,
		RepoName:           project.Name,
		Branch:             branch,
		CommitHash:         pipeline.CommitHash,
		AccessToken:        project.AccessToken,
		PipelineFilename:   pipelineFilename,
		DeploymentFilename: deploymentFilename,
		ProjectID:          project.ID,
		PipelineID:         pipeline.ID,
	}

	s.runPipelineLogic(params)
}
