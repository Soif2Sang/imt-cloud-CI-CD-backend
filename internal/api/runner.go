package api

import (
	"bufio"
	"fmt"
	"io"
	"log"
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
)

const deployScript = `#!/bin/bash
set -e # Stop script on first error

# Export variables
export PN=$1
export CF=$2
export OF=$3

# Docker commands
echo "Tearing down old containers..."
docker compose -p $PN -f $CF -f $OF down --remove-orphans

echo "Pulling new images..."
docker compose -p $PN -f $CF -f $OF pull

echo "Starting containers..."
docker compose -p $PN -f $CF -f $OF up -d --wait

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

	log.Printf("Starting pipeline for %s", params.RepoName)

	// Clone the repository
	log.Printf("Cloning repository to %s", workspaceDir)
	if err := git.Clone(params.RepoURL, params.Branch, workspaceDir, params.AccessToken, params.CommitHash); err != nil {
		log.Printf("Failed to clone repository: %v", err)
		if s.db != nil && params.PipelineID > 0 {
			s.db.UpdatePipelineStatus(params.PipelineID, "failed")
		}
		return
	}
	defer git.Cleanup(workspaceDir)

	// Find and parse the CI config file
	configPath := filepath.Join(workspaceDir, params.PipelineFilename)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Printf("CI config file not found at %s", configPath)
		if s.db != nil && params.PipelineID > 0 {
			s.db.UpdatePipelineStatus(params.PipelineID, "failed")
		}
		return
	}

	log.Printf("Found CI config: %s", configPath)

	// Parse the CI config
	p := pipeline.NewParser(configPath)
	config, err := p.Parse()
	if err != nil {
		log.Printf("Failed to parse CI config: %v", err)
		if s.db != nil && params.PipelineID > 0 {
			s.db.UpdatePipelineStatus(params.PipelineID, "failed")
		}
		return
	}

	log.Printf("Config loaded with %d stages", len(config.Stages))

	// Pre-create jobs and deployment for visualization
	if s.db != nil && params.PipelineID > 0 {
		// Pre-create jobs
		for _, stageName := range config.Stages {
			for jobName, job := range config.Jobs {
				if job.Stage == stageName {
					if _, err := s.db.CreateJob(params.PipelineID, jobName, job.Stage, job.Image); err != nil {
						log.Printf("Failed to pre-create job %s: %v", jobName, err)
					}
				}
			}
		}
		// Pre-create deployment
		if _, err := s.db.CreatePendingDeployment(params.PipelineID); err != nil {
			log.Printf("Failed to pre-create deployment: %v", err)
		}
	}

	// Execute the pipeline jobs
	pipelineSuccess := s.executePipeline(config, workspaceDir, params.PipelineID, project)

	// Deploy if successful
	if pipelineSuccess {
		log.Printf("Pipeline successful. Starting deployment using %s...", params.DeploymentFilename)

		var deploymentID int
		if s.db != nil && params.PipelineID > 0 {
			deploy, err := s.db.GetDeploymentByPipeline(params.PipelineID)
			if err != nil {
				// Fallback if not found
				deploy, err = s.db.CreateDeployment(params.PipelineID)
				if err != nil {
					log.Printf("Failed to create deployment record: %v", err)
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
			log.Printf("Deployment failed: %v", err)

			// Attempt Rollback
			rollbackSuccess := false
			if s.db != nil && project != nil {
				lastPipeline, _ := s.db.GetLastSuccessfulPipeline(project.ID)
				if lastPipeline != nil && lastPipeline.CommitHash != "" {
					log.Printf("Attempting rollback to commit %s", lastPipeline.CommitHash)

					// Prepare rollback params
					rollbackParams := params
					rollbackParams.CommitHash = lastPipeline.CommitHash
					// Note: We use the same config filenames as current project settings.

					// Create unique workspace for rollback
					rollbackDir := filepath.Join("/tmp", "cicd-workspaces", fmt.Sprintf("%s-rollback-%s-%d", params.RepoName, rollbackParams.CommitHash[:8], time.Now().Unix()))

					log.Printf("Cloning rollback commit to %s", rollbackDir)
					if cloneErr := git.Clone(rollbackParams.RepoURL, rollbackParams.Branch, rollbackDir, rollbackParams.AccessToken, rollbackParams.CommitHash); cloneErr == nil {
						defer git.Cleanup(rollbackDir)

						// Log rollback start
						s.db.CreateDeploymentLog(params.PipelineID, "=== ROLLBACK STARTED ===")

						// Run deployment for old version (logs are streamed)
						_, rbErr := s.deployToEnv(project, rollbackParams, rollbackDir)

						if rbErr == nil {
							rollbackSuccess = true
							log.Printf("Rollback successful")
						} else {
							log.Printf("Rollback failed: %v", rbErr)
						}
					} else {
						log.Printf("Rollback clone failed: %v", cloneErr)
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
			log.Printf("Deployment successful!")
			if s.db != nil && deploymentID > 0 {
				s.db.UpdateDeploymentStatus(deploymentID, "success")
			}
		}
	}

	// Update final pipeline status
	if s.db != nil && params.PipelineID > 0 {
		if pipelineSuccess {
			s.db.UpdatePipelineStatus(params.PipelineID, "success")
			log.Printf("Pipeline %d completed successfully", params.PipelineID)
		} else {
			s.db.UpdatePipelineStatus(params.PipelineID, "failed")
			log.Printf("Pipeline %d failed", params.PipelineID)

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
				log.Printf("Failed to fetch project variables: %v", err)
			} else {
				for _, v := range variables {
					envVars = append(envVars, fmt.Sprintf("%s=%s", v.Key, v.Value))
				}
			}
		}
	}

	for _, stageName := range config.Stages {
		log.Printf("Running stage: %s", stageName)

		for jobName, job := range config.Jobs {
			if job.Stage != stageName {
				continue
			}

			log.Printf("Running job: %s (image: %s)", jobName, job.Image)

			// Update job status in database
			var jobID int
			if s.db != nil && pipelineID > 0 {
				dbJob, err := s.db.GetJobByName(pipelineID, jobName)
				if err != nil {
					log.Printf("Job not found, creating: %v", err)
					dbJob, err = s.db.CreateJob(pipelineID, jobName, job.Stage, job.Image)
				}

				if err == nil && dbJob != nil {
					jobID = dbJob.ID
					s.db.UpdateJobStatus(jobID, "running", nil)
				} else {
					log.Printf("Failed to get/create job record: %v", err)
				}
			}

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

	// Use a pipe to connect stdcopy (writer) to scanner (reader)
	pr, pw := io.Pipe()

	// Run stdcopy in a goroutine to demultiplex the docker stream
	go func() {
		// We write both stdout and stderr to the same pipe
		if _, err := stdcopy.StdCopy(pw, pw, reader); err != nil {
			log.Printf("Error demultiplexing logs: %v", err)
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
			log.Printf("Project not found for repo %s: %v. Ignoring webhook.", pushEvent.Repository.CloneURL, err)
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
			log.Printf("Failed to create pipeline record: %v", err)
		} else {
			pipelineID = pipeline.ID
			log.Printf("Pipeline created with ID: %d", pipelineID)
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
	log.Printf("Starting manual pipeline %d for project %s", pipeline.ID, project.Name)

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
	var fullLogs strings.Builder
	var err error

	// Helper to log locally, append to return string, and stream to DB
	logAndStream := func(msg string) {
		// Append to builder (for return value / rollback logs)
		fullLogs.WriteString(msg + "\n")

		// Stream to DB
		if s.db != nil && params.PipelineID > 0 {
			// Insert raw line
			if dbErr := s.db.CreateDeploymentLog(params.PipelineID, msg); dbErr != nil {
				log.Printf("Error streaming log to DB: %v", dbErr)
			}
		}
		
		log.Printf(msg)
	}

	// Helper for block logging (when we get a big chunk, split it for streaming)
	logBlock := func(blockName, content string) {
		header := fmt.Sprintf("=== %s ===", blockName)
		logAndStream(header)

		lines := strings.Split(content, "\n")
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				logAndStream(line)
			}
		}
		
		log.Printf(content)
	}

	// Check if we should use Registry/SSH flow
	if project != nil && project.RegistryUser != "" {
		logAndStream("Using Registry/SSH deployment flow")

		// 1. Generate docker-compose.override.yml
		composePath := filepath.Join(workspaceDir, params.DeploymentFilename)
		services, parseErr := compose.ParseServices(composePath)
		if parseErr != nil {
			err = fmt.Errorf("failed to parse compose services: %w", parseErr)
			logAndStream(err.Error())
		} else {
			overrideContent, genErr := compose.GenerateOverride(services, project.RegistryUser, params.RepoName, params.CommitHash)
			if genErr != nil {
				err = fmt.Errorf("failed to generate override: %w", genErr)
				logAndStream(err.Error())
			} else {
				overrideFilename := "docker-compose.override.yml"
				os.WriteFile(filepath.Join(workspaceDir, overrideFilename), overrideContent, 0644)
				logAndStream(fmt.Sprintf("Generated %s", overrideFilename))

				// 2. Login to Registry
				if loginErr := s.docker.Login(project.RegistryUser, project.RegistryToken, ""); loginErr != nil {
					err = fmt.Errorf("registry login failed: %w", loginErr)
					logAndStream(err.Error())
				} else {
					logAndStream(fmt.Sprintf("Logged in to registry as %s", project.RegistryUser))

					// 3. Build with Override
					logAndStream("Building images...")
					buildLogs, buildErr := s.docker.ComposeBuild(workspaceDir, params.DeploymentFilename, overrideFilename)
					logBlock("BUILD LOGS", buildLogs)

					if buildErr != nil {
						err = buildErr
					} else {
						// 4. Push with Override
						logAndStream("Pushing images...")
						pushLogs, pushErr := s.docker.ComposePush(workspaceDir, params.DeploymentFilename, overrideFilename)
						logBlock("PUSH LOGS", pushLogs)

						if pushErr != nil {
							err = pushErr
						} else {
							// 5. Remote Deploy via SSH
							if project.SSHHost != "" {
								client, sshErr := ssh.NewClient(project.SSHHost, project.SSHUser, project.SSHPrivateKey)
								if sshErr != nil {
									err = fmt.Errorf("ssh connection failed: %w", sshErr)
									logAndStream(err.Error())
								} else {
									logAndStream(fmt.Sprintf("Connected via SSH to %s", project.SSHHost))
									defer client.Close()

									sanitizedRepoName := sanitizeProjectName(params.RepoName)
									remoteDir := fmt.Sprintf("deploy/%s", sanitizedRepoName)
									client.RunCommand("mkdir -p " + remoteDir)

									// Copy files
									composeContent, _ := os.ReadFile(composePath)
									client.CopyFile(composeContent, remoteDir+"/"+params.DeploymentFilename)
									client.CopyFile(overrideContent, remoteDir+"/"+overrideFilename)

									logAndStream(fmt.Sprintf("Copied config files to remote dir: %s", remoteDir))

									// Deploy command
									// Add /usr/local/bin to PATH for non-interactive shells where docker might not be found
									//                                                                        cmd := fmt.Sprintf("export PATH=$PATH:/usr/local/bin:/usr/bin && cd %s && export CF=%s && export OF=%s && export PN=%s && docker compose -p $PN -f $CF -f $OF down --remove-orphans && docker compose -p $PN -f $CF -f $OF pull && docker compose -p $PN -f $CF -f $OF up -d --wait && sleep 5 && echo 'Checking container statuses...' && docker compose -p $PN -f $CF -f $OF ps -a && echo '--- Detailed Health Check ---' && INSPECT_OUTPUT=$(docker compose -p $PN -f $CF -f $OF ps -a -q | xargs docker inspect -f '{{.Name}} | Status: {{.State.Status}} | Running: {{.State.Running}} | ExitCode: {{.State.ExitCode}}' 2>/dev/null || true) && echo \"$INSPECT_OUTPUT\" && FAILED_CONTAINERS=$(echo \"$INSPECT_OUTPUT\" | grep -v 'Running: true') && if [ -n \"$FAILED_CONTAINERS\" ]; then echo '--- Deployment Failed: Unhealthy Containers Detected ---' && echo \"$FAILED_CONTAINERS\" && echo '--- Logs ---' && docker compose -p $PN -f $CF -f $OF logs && exit 1; else echo '--- Health Check Passed ---'; fi",
									//                                                                                remoteDir, params.DeploymentFilename, overrideFilename, sanitizedRepoName)									
									// logAndStream("=== REMOTE DEPLOY LOGS ===")
									// remoteErr := client.RunCommandStream(cmd, func(line string) {
									// 	logAndStream(line)
									// })
									// 
									// // 1. Upload the script
									// 
									client.CopyFile([]byte(deployScript), remoteDir+"/deploy.sh")
									client.RunCommand("chmod +x " + remoteDir + "/deploy.sh")
									
									// 2. Run the script with arguments
									// Usage: ./deploy.sh [ProjectName] [ComposeFile] [OverrideFile]
									cmd := fmt.Sprintf("export PATH=$PATH:/usr/local/bin:/usr/bin && cd %s && ./deploy.sh %s %s %s", 
									    remoteDir, sanitizedRepoName, params.DeploymentFilename, overrideFilename)
									
									remoteErr := client.RunCommandStream(cmd, func(line string) {
									    logAndStream(line)
									})

									if remoteErr != nil {
										logAndStream(fmt.Sprintf("Remote command error: %v", remoteErr))
									}
									err = remoteErr
								}
							} else {
								logAndStream("No SSH host configured, skipping remote deployment.")
							}
						}
					}
				}
			}
		}
	} else {
		// Fallback to local deployment
		logAndStream("Using local deployment flow")
		sanitizedRepoName := sanitizeProjectName(params.RepoName)
		localLogs, localErr := s.docker.DeployCompose(workspaceDir, params.DeploymentFilename, sanitizedRepoName)
		logAndStream(localLogs)
		err = localErr
	}

	return fullLogs.String(), err
}
