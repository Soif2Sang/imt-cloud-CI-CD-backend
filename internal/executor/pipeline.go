package executor

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/pkg/stdcopy"

	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/database"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/docker"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/models"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/parser/pipeline"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/pkg/logger"
)

type PipelineExecutor struct {
	db     *database.DB
	docker *docker.DockerExecutor
}

func NewPipelineExecutor(db *database.DB, docker *docker.DockerExecutor) *PipelineExecutor {
	return &PipelineExecutor{
		db:     db,
		docker: docker,
	}
}

// Execute runs all jobs in the pipeline
func (e *PipelineExecutor) Execute(config *pipeline.PipelineConfig, workspaceDir string, pipelineID int, project *models.Project) bool {
	pipelineSuccess := true

	// Prepare environment variables
	var envVars []string
	if project != nil {
		// Inject Custom Variables (Secrets/Env Vars)
		if e.db != nil {
			variables, err := e.db.GetVariablesByProject(project.ID)
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
			if e.db != nil && pipelineID > 0 {
				dbJob, err := e.db.GetJobByName(pipelineID, jobName)
				if err != nil {
					logger.Warn(fmt.Sprintf("Job not found, creating: %v", err))
					dbJob, err = e.db.CreateJob(pipelineID, jobName, job.Stage, job.Image)
				}

				if err == nil && dbJob != nil {
					jobID = dbJob.ID
					e.db.UpdateJobStatus(jobID, "running", nil)
				} else {
					logger.Error(fmt.Sprintf("Failed to get/create job record: %v", err))
				}
			}

			// Pull the image
			logger.Info(fmt.Sprintf("Pulling image: %s", job.Image))
			if err := e.docker.PullImage(job.Image); err != nil {
				logger.Error(fmt.Sprintf("Failed to pull image %s: %v", job.Image, err))
				if e.db != nil && jobID > 0 {
					exitCode := 1
					e.db.UpdateJobStatus(jobID, "failed", &exitCode)
				}
				pipelineSuccess = false
				continue
			}

			// Run the job with workspace mounted
			containerID, err := e.docker.RunJobWithVolume(job.Image, job.Script, workspaceDir, envVars)
			if err != nil {
				logger.Error(fmt.Sprintf("Failed to start job %s: %v", jobName, err))
				if e.db != nil && jobID > 0 {
					exitCode := 1
					e.db.UpdateJobStatus(jobID, "failed", &exitCode)
				}
				pipelineSuccess = false
				continue
			}

			// Collect and store logs
			e.collectLogs(containerID, jobID)

			// Wait for container to finish
			statusCode, err := e.docker.WaitForContainer(containerID)
			if err != nil {
				logger.Error(fmt.Sprintf("Error waiting for container: %v", err))
			}

			// Update job status
			exitCode := int(statusCode)
			if e.db != nil && jobID > 0 {
				status := "success"
				if statusCode != 0 {
					status = "failed"
				}
				e.db.UpdateJobStatus(jobID, status, &exitCode)
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
func (e *PipelineExecutor) collectLogs(containerID string, jobID int) {
	reader, err := e.docker.GetLogs(containerID)
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
		if len(logBatch) >= 10 && e.db != nil && jobID > 0 {
			if err := e.db.CreateLogBatch(jobID, logBatch); err != nil {
				logger.Error(fmt.Sprintf("Failed to store logs: %v", err))
			}
			logBatch = nil
		}
	}

	// Store remaining logs
	if len(logBatch) > 0 && e.db != nil && jobID > 0 {
		if err := e.db.CreateLogBatch(jobID, logBatch); err != nil {
			logger.Error(fmt.Sprintf("Failed to store remaining logs: %v", err))
		}
	}
}
