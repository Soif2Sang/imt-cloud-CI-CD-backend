package api

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/pkg/stdcopy"

	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/git"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/models"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/parser/compose"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/parser/pipeline"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/ssh"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/pkg/logger"
)

const deployScript = `#!/bin/bash
set -e # Stop script on first error

echo "--- DEPLOYMENT SCRIPT v2 ---"

# Export variables
export PN=$1
export CF=$2
export OF=$3

# Docker commands
echo "Tearing down old containers..."
docker compose -p $PN down --remove-orphans

echo "Pulling new images..."
docker compose -p $PN -f $CF -f $OF pull

echo "Starting containers..."
docker compose -p $PN -f $CF -f $OF up -d --force-recreate --wait

echo "Waiting for stabilization..."
sleep 5

echo "--- Detailed Health Check ---"
# Get status of all containers
INSPECT_OUTPUT=$(docker compose -p $PN -f $CF -f $OF ps -a -q | xargs docker inspect -f '{{.Name}} | Status: {{.State.Status}} | Running: {{.State.Running}} | ExitCode: {{.State.ExitCode}}' 2>/dev/null || true)

echo "$INSPECT_OUTPUT"

# Filter for unhealthy containers (The fix is here: || true)
FAILED_CONTAINERS=$(echo "$INSPECT_OUTPUT" | grep -v 'Running: true' || true)

if [ -n "$FAILED_CONTAINERS" ]; then
    echo "--- Deployment Failed: Unhealthy Containers Detected ---"
    echo "$FAILED_CONTAINERS"
    echo "--- Logs ---"
    docker compose -p $PN -f $CF -f $OF logs
    exit 1
else
    echo "--- Health Check Passed ---"
fi
`

// runPipeline executes the CI/CD pipeline logic
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

	// Execute the pipeline jobs
	pipelineSuccess := s.executePipeline(config, workspaceDir, params.PipelineID, project)

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

		// Deploy to environment (logs are streamed to DB)
		_, err := s.deployToEnv(project, params, workspaceDir)

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

						// Run deployment for old version (logs are streamed)
						_, rbErr := s.deployToEnv(project, rollbackParams, rollbackDir)

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
			if deploy, err := s.db.GetDeploymentByPipeline(params.PipelineID); err == nil && deploy != nil {
				s.db.UpdateDeploymentStatus(deploy.ID, "failed")
			}
		}
	}
}

// executePipeline runs all jobs in the pipeline
func (s *Server) executePipeline(config *pipeline.PipelineConfig, workspaceDir string, pipelineID int, project *models.Project) bool {
	pipelineSuccess := true

	// Prepare environment variables
	var envVars []string
	if project != nil {
		// Inject Custom Variables (Secrets/Env Vars)
		if s.db != nil {
			variables, err := s.db.GetVariablesByProject(project.ID)
			if err != nil {
				logger.Error("Failed to fetch project variables: " + err.Error())
			} else {
				for _, v := range variables {
					envVars = append(envVars, fmt.Sprintf("%s=%s", v.Key, v.Value))
				}
			}
		}
	}

	for _, stageName := range config.Stages {
		logger.Info(fmt.Sprintf("Running stage: %s", stageName))

		for jobName, job := range config.Jobs {
			if job.Stage != stageName {
				continue
			}

			logger.Info(fmt.Sprintf("Running job: %s (image: %s)", jobName, job.Image))

			// Update job status in database
			var jobID int
			if s.db != nil && pipelineID > 0 {
				dbJob, err := s.db.GetJobByName(pipelineID, jobName)
				if err != nil {
					logger.Warn(fmt.Sprintf("Job not found, creating: %v", err))
					dbJob, err = s.db.CreateJob(pipelineID, jobName, job.Stage, job.Image)
				}

				if err == nil && dbJob != nil {
					jobID = dbJob.ID
					s.db.UpdateJobStatus(jobID, "running", nil)
				} else {
					logger.Error(fmt.Sprintf("Failed to get/create job record: %v", err))
				}
			}

			// Pull the image
			logger.Info(fmt.Sprintf("Pulling image: %s", job.Image))
			if err := s.docker.PullImage(job.Image); err != nil {
				logger.Error(fmt.Sprintf("Failed to pull image %s: %v", job.Image, err))
				if s.db != nil && jobID > 0 {
					exitCode := 1
					s.db.UpdateJobStatus(jobID, "failed", &exitCode)
				}
				pipelineSuccess = false
				continue
			}

			// Run the job with workspace mounted
			containerID, err := s.docker.RunJobWithVolume(job.Image, job.Script, workspaceDir, envVars)
			if err != nil {
				logger.Error(fmt.Sprintf("Failed to start job %s: %v", jobName, err))
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
				logger.Error(fmt.Sprintf("Error waiting for container: %v", err))
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
				logger.Error(fmt.Sprintf("Job %s failed with exit code %d", jobName, statusCode))
				pipelineSuccess = false
				// Stop pipeline on first failure
				return false
			}

			logger.Info(fmt.Sprintf("Job %s completed successfully", jobName))
		}
	}

	return pipelineSuccess
}

// collectLogs collects logs from the container and stores them in the database
func (s *Server) collectLogs(containerID string, jobID int) {
	reader, err := s.docker.GetLogs(containerID)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to get logs: %v", err))
		return
	}
	defer reader.Close()

	// Use a pipe to connect stdcopy (writer) to scanner (reader)
	pr, pw := io.Pipe()

	// Run stdcopy in a goroutine to demultiplex the docker stream
	go func() {
		// We write both stdout and stderr to the same pipe
		if _, err := stdcopy.StdCopy(pw, pw, reader); err != nil {
			logger.Error(fmt.Sprintf("Error demultiplexing logs: %v", err))
		}
		pw.Close()
	}()

	scanner := bufio.NewScanner(pr)
	var logBatch []string

	for scanner.Scan() {
		line := scanner.Text()

		// Sanitize line: remove null bytes (Postgres doesn't allow them in text)
		cleanLine := strings.ReplaceAll(line, "\x00", "")

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
				logger.Error(fmt.Sprintf("Failed to store logs: %v", err))
			}
			logBatch = nil
		}
	}

	// Store remaining logs
	if len(logBatch) > 0 && s.db != nil && jobID > 0 {
		if err := s.db.CreateLogBatch(jobID, logBatch); err != nil {
			logger.Error(fmt.Sprintf("Failed to store remaining logs: %v", err))
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
		project, err := s.findOrCreateProject(pushEvent.Repository)
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

// deployToEnv handles the deployment logic (Registry/SSH or Local)
func (s *Server) deployToEnv(project *models.Project, params models.PipelineRunParams, workspaceDir string) (string, error) {
	dLogger := s.newDeploymentLogger(params.PipelineID)

	var err error
	// Check if we should use Registry/SSH flow
	if project != nil && project.RegistryUser != "" && project.SSHHost != "" {
		err = s.deployRemote(project, params, workspaceDir, dLogger)
	} else {
		err = s.deployLocal(params, workspaceDir, dLogger)
	}

	return dLogger.String(), err
}

// deployLocal handles execution on the same machine
func (s *Server) deployLocal(params models.PipelineRunParams, workspaceDir string, dLogger *DeploymentLogger) error {
	dLogger.Log("Using local deployment flow")
	sanitizedRepoName := sanitizeProjectName(params.RepoName)
	localLogs, localErr := s.docker.DeployCompose(workspaceDir, params.DeploymentFilename, sanitizedRepoName)
	dLogger.Log(localLogs)
	return localErr
}

// deployRemote handles the build-push-deploy-ssh flow
func (s *Server) deployRemote(project *models.Project, params models.PipelineRunParams, workspaceDir string, dLogger *DeploymentLogger) error {
	dLogger.Log("Using Registry/SSH deployment flow")

	// 1. Generate docker-compose.override.yml
	overrideFilename := "docker-compose.override.yml"
	overrideContent, err := s.generateOverride(project, params, workspaceDir, overrideFilename, dLogger)
	if err != nil {
		return err
	}

	// 2. Build and Push Images
	if err := s.buildAndPushImages(project, params, workspaceDir, overrideFilename, dLogger); err != nil {
		return err
	}

	// 3. Remote Deploy via SSH
	return s.executeRemoteSSH(project, params, workspaceDir, overrideFilename, overrideContent, dLogger)
}

// generateOverride creates the compose override file for registry usage
func (s *Server) generateOverride(project *models.Project, params models.PipelineRunParams, workspaceDir, overrideFilename string, dLogger *DeploymentLogger) ([]byte, error) {
	composePath := filepath.Join(workspaceDir, params.DeploymentFilename)
	services, parseErr := compose.ParseServices(composePath)
	if parseErr != nil {
		err := fmt.Errorf("failed to parse compose services: %w", parseErr)
		dLogger.Log(err.Error())
		return nil, err
	}

	overrideContent, genErr := compose.GenerateOverride(services, project.RegistryUser, params.RepoName, params.CommitHash)
	if genErr != nil {
		err := fmt.Errorf("failed to generate override: %w", genErr)
		dLogger.Log(err.Error())
		return nil, err
	}

	if err := os.WriteFile(filepath.Join(workspaceDir, overrideFilename), overrideContent, 0644); err != nil {
		dLogger.Log("Failed to write override file: " + err.Error())
		return nil, err
	}
	dLogger.Log(fmt.Sprintf("Generated %s", overrideFilename))
	return overrideContent, nil
}

// buildAndPushImages logs into registry, builds, and pushes images
func (s *Server) buildAndPushImages(project *models.Project, params models.PipelineRunParams, workspaceDir, overrideFilename string, dLogger *DeploymentLogger) error {
	// Login
	if loginErr := s.docker.Login(project.RegistryUser, project.RegistryToken, ""); loginErr != nil {
		err := fmt.Errorf("registry login failed: %w", loginErr)
		dLogger.Log(err.Error())
		return err
	}
	dLogger.Log(fmt.Sprintf("Logged in to registry as %s", project.RegistryUser))

	// Build
	dLogger.Log("Building images...")
	buildLogs, buildErr := s.docker.ComposeBuild(workspaceDir, params.DeploymentFilename, overrideFilename)
	dLogger.LogBlock("BUILD LOGS", buildLogs)
	if buildErr != nil {
		return buildErr
	}

	// Push
	dLogger.Log("Pushing images...")
	pushLogs, pushErr := s.docker.ComposePush(workspaceDir, params.DeploymentFilename, overrideFilename)
	dLogger.LogBlock("PUSH LOGS", pushLogs)
	if pushErr != nil {
		return pushErr
	}

	return nil
}

// executeRemoteSSH handles the SSH connection and remote command execution
func (s *Server) executeRemoteSSH(project *models.Project, params models.PipelineRunParams, workspaceDir, overrideFilename string, overrideContent []byte, dLogger *DeploymentLogger) error {
	if project.SSHHost == "" {
		dLogger.Log("No SSH host configured, skipping remote deployment.")
		return nil // Or error? Logic in original was "skip" but effectively success or just doing nothing.
	}

	client, sshErr := ssh.NewClient(project.SSHHost, project.SSHUser, project.SSHPrivateKey)
	if sshErr != nil {
		err := fmt.Errorf("ssh connection failed: %w", sshErr)
		dLogger.Log(err.Error())
		return err
	}
	defer client.Close()
	dLogger.Log(fmt.Sprintf("Connected via SSH to %s", project.SSHHost))

	sanitizedRepoName := sanitizeProjectName(params.RepoName)
	remoteDir := fmt.Sprintf("deploy/%s", sanitizedRepoName)
	client.RunCommand("mkdir -p " + remoteDir)

	// Copy files
	composePath := filepath.Join(workspaceDir, params.DeploymentFilename)
	composeContent, _ := os.ReadFile(composePath) // Error ignored in original, assuming file exists if parsed earlier
	client.CopyFile(composeContent, remoteDir+"/"+params.DeploymentFilename)
	client.CopyFile(overrideContent, remoteDir+"/"+overrideFilename)

	dLogger.Log(fmt.Sprintf("Copied config files to remote dir: %s", remoteDir))

	// Upload deploy script
	client.CopyFile([]byte(deployScript), remoteDir+"/deploy.sh")
	client.RunCommand("chmod +x " + remoteDir + "/deploy.sh")
	
	logger.Debug(fmt.Sprintf("The sanitizedRepoName %s", sanitizedRepoName))

	// Run script
	cmd := fmt.Sprintf("export PATH=$PATH:/usr/local/bin:/usr/bin && cd %s && ./deploy.sh %s %s %s",
		remoteDir, sanitizedRepoName, params.DeploymentFilename, overrideFilename)

	remoteErr := client.RunCommandStream(cmd, func(line string) {
		dLogger.Log(line)
	})

	if remoteErr != nil {
		dLogger.Log(fmt.Sprintf("Remote command error: %v", remoteErr))
		return remoteErr
	}

	return nil
}

// === Deployment Helper Struct ===

type DeploymentLogger struct {
	server     *Server
	pipelineID int
	logs       strings.Builder
}

func (s *Server) newDeploymentLogger(pipelineID int) *DeploymentLogger {
	return &DeploymentLogger{
		server:     s,
		pipelineID: pipelineID,
	}
}

func (dLogger *DeploymentLogger) Log(msg string) {
	// 1. Append to local builder (for return)
	dLogger.logs.WriteString(msg + "\n")

	// 2. Stream to DB
	if dLogger.server.db != nil && dLogger.pipelineID > 0 {
		if dbErr := dLogger.server.db.CreateDeploymentLog(dLogger.pipelineID, msg); dbErr != nil {
			logger.Error(fmt.Sprintf("Error streaming log to DB: %v", dbErr))
		}
	}

	// 3. System Log
	logger.Info(msg)
}

func (dLogger *DeploymentLogger) LogBlock(blockName, content string) {
	dLogger.Log(fmt.Sprintf("=== %s ===", blockName))
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			dLogger.Log(line)
		}
	}
}

func (dLogger *DeploymentLogger) String() string {
	return dLogger.logs.String()
}
