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

	// Execute the pipeline jobs
	pipelineSuccess := s.executePipeline(config, workspaceDir, params.PipelineID)

	// Deploy if successful
	if pipelineSuccess {
		log.Printf("Pipeline successful. Starting deployment using %s...", params.DeploymentFilename)

		var deploymentID int
		if s.db != nil && params.PipelineID > 0 {
			deploy, err := s.db.CreateDeployment(params.PipelineID)
			if err != nil {
				log.Printf("Failed to create deployment record: %v", err)
			} else {
				deploymentID = deploy.ID
			}
		}

		var logs string
		var err error

		// Check if we should use Registry/SSH flow
		if project != nil && project.RegistryUser != "" {
			log.Printf("Using Registry/SSH deployment flow")

			// 1. Generate docker-compose.override.yml
			composePath := filepath.Join(workspaceDir, params.DeploymentFilename)
			services, parseErr := compose.ParseServices(composePath)
			if parseErr != nil {
				err = fmt.Errorf("failed to parse compose services: %w", parseErr)
				logs += err.Error() + "\n"
			} else {
				overrideContent, genErr := compose.GenerateOverride(services, project.RegistryUser, params.RepoName, params.CommitHash)
				if genErr != nil {
					err = fmt.Errorf("failed to generate override: %w", genErr)
					logs += err.Error() + "\n"
				} else {
					overrideFilename := "docker-compose.override.yml"
					os.WriteFile(filepath.Join(workspaceDir, overrideFilename), overrideContent, 0644)
					log.Printf("Generated %s", overrideFilename)
					logs += fmt.Sprintf("Generated %s\n", overrideFilename)

					// 2. Login to Registry
					if loginErr := s.docker.Login(project.RegistryUser, project.RegistryToken, ""); loginErr != nil {
						err = fmt.Errorf("registry login failed: %w", loginErr)
						logs += err.Error() + "\n"
					} else {
						log.Printf("Logged in to registry as %s", project.RegistryUser)
						logs += fmt.Sprintf("Logged in to registry as %s\n", project.RegistryUser)

						// 3. Build with Override
						buildLogs, buildErr := s.docker.ComposeBuild(workspaceDir, params.DeploymentFilename, overrideFilename)
						logs += "=== BUILD LOGS ===\n" + buildLogs + "\n"
						if buildErr != nil {
							err = buildErr
						} else {
							// 4. Push with Override
							pushLogs, pushErr := s.docker.ComposePush(workspaceDir, params.DeploymentFilename, overrideFilename)
							logs += "=== PUSH LOGS ===\n" + pushLogs + "\n"
							if pushErr != nil {
								err = pushErr
							} else {
								// 5. Remote Deploy via SSH
								if project.SSHHost != "" {
									client, sshErr := ssh.NewClient(project.SSHHost, project.SSHUser, project.SSHPrivateKey)
									if sshErr != nil {
										err = fmt.Errorf("ssh connection failed: %w", sshErr)
										logs += err.Error() + "\n"
									} else {
										log.Printf("Connected via SSH to %s", project.SSHHost)
										logs += fmt.Sprintf("Connected via SSH to %s\n", project.SSHHost)
										defer client.Close()

										remoteDir := fmt.Sprintf("deploy/%s", sanitizeProjectName(params.RepoName))
										client.RunCommand("mkdir -p " + remoteDir)

										// Copy files
										composeContent, _ := os.ReadFile(composePath)
										client.CopyFile(composeContent, remoteDir+"/"+params.DeploymentFilename)
										client.CopyFile(overrideContent, remoteDir+"/"+overrideFilename)

										log.Printf("Copied config files to remote dir: %s", remoteDir)
										logs += fmt.Sprintf("Copied config files to remote dir: %s\n", remoteDir)

										// Deploy command
										// Add /usr/local/bin to PATH for non-interactive shells where docker might not be found
										cmd := fmt.Sprintf("export PATH=$PATH:/usr/local/bin:/usr/bin && cd %s && docker compose -f %s -f %s pull && docker compose -f %s -f %s up -d",
											remoteDir, params.DeploymentFilename, overrideFilename,
											params.DeploymentFilename, overrideFilename)

										remoteLogs, remoteErr := client.RunCommand(cmd)
										logs += "=== REMOTE DEPLOY LOGS ===\n" + remoteLogs + "\n"
										if remoteErr != nil {
											log.Printf("Remote logs:\n%s", remoteLogs)
										}
										err = remoteErr
									}
								} else {
									logs += "No SSH host configured, skipping remote deployment.\n"
								}
							}
						}
					}
				}
			}
		} else {
			// Fallback to local deployment
			log.Printf("Using local deployment flow")
			sanitizedRepoName := sanitizeProjectName(params.RepoName)
			logs, err = s.docker.DeployCompose(workspaceDir, params.DeploymentFilename, sanitizedRepoName)
		}

		// Store logs
		if s.db != nil && params.PipelineID > 0 && logs != "" {
			if logErr := s.db.CreateDeploymentLog(params.PipelineID, logs); logErr != nil {
				log.Printf("Failed to store deployment logs: %v", logErr)
			}
		}

		if err != nil {
			log.Printf("Deployment failed: %v", err)
			pipelineSuccess = false
			if s.db != nil && deploymentID > 0 {
				s.db.UpdateDeploymentStatus(deploymentID, "failed")
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
		}
	}
}

// executePipeline runs all jobs in the pipeline
func (s *Server) executePipeline(config *pipeline.PipelineConfig, workspaceDir string, pipelineID int) bool {
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
			containerID, err := s.docker.RunJobWithVolume(job.Image, job.Script, workspaceDir)
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
			log.Printf("Failed to find/create project: %v", err)
		} else {
			projectID = project.ID
			accessToken = project.AccessToken
			pipelineFilename = project.PipelineFilename
			deploymentFilename = project.DeploymentFilename
		}
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
