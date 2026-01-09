package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/database"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/docker"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/models"
	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/parser/compose"
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

type DeploymentExecutor struct {
	db     *database.DB
	docker *docker.DockerExecutor
}

func NewDeploymentExecutor(db *database.DB, docker *docker.DockerExecutor) *DeploymentExecutor {
	return &DeploymentExecutor{
		db:     db,
		docker: docker,
	}
}

// Execute handles the deployment logic (Registry/SSH or Local)
func (e *DeploymentExecutor) Execute(project *models.Project, params models.PipelineRunParams, workspaceDir string) (string, error) {
	dLogger := e.newDeploymentLogger(params.PipelineID)

	var err error
	// Check if we should use Registry/SSH flow
	if project != nil && project.RegistryUser != "" && project.SSHHost != "" {
		err = e.deployRemote(project, params, workspaceDir, dLogger)
	} else {
		err = e.deployLocal(params, workspaceDir, dLogger)
	}

	return dLogger.String(), err
}

// deployLocal handles execution on the same machine
func (e *DeploymentExecutor) deployLocal(params models.PipelineRunParams, workspaceDir string, dLogger *DeploymentLogger) error {
	dLogger.Log("Using local deployment flow")
	sanitizedRepoName := sanitizeProjectName(params.RepoName)
	localLogs, localErr := e.docker.DeployCompose(workspaceDir, params.DeploymentFilename, sanitizedRepoName)
	dLogger.Log(localLogs)
	return localErr
}

// deployRemote handles the build-push-deploy-ssh flow
func (e *DeploymentExecutor) deployRemote(project *models.Project, params models.PipelineRunParams, workspaceDir string, dLogger *DeploymentLogger) error {
	dLogger.Log("Using Registry/SSH deployment flow")

	// 1. Generate docker-compose.override.yml
	overrideFilename := "docker-compose.override.yml"
	overrideContent, err := e.generateOverride(project, params, workspaceDir, overrideFilename, dLogger)
	if err != nil {
		return err
	}

	// 2. Build and Push Images
	if err := e.buildAndPushImages(project, params, workspaceDir, overrideFilename, dLogger); err != nil {
		return err
	}

	// 3. Remote Deploy via SSH
	return e.executeRemoteSSH(project, params, workspaceDir, overrideFilename, overrideContent, dLogger)
}

// generateOverride creates the compose override file for registry usage
func (e *DeploymentExecutor) generateOverride(project *models.Project, params models.PipelineRunParams, workspaceDir, overrideFilename string, dLogger *DeploymentLogger) ([]byte, error) {
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
func (e *DeploymentExecutor) buildAndPushImages(project *models.Project, params models.PipelineRunParams, workspaceDir, overrideFilename string, dLogger *DeploymentLogger) error {
	// Login
	if loginErr := e.docker.Login(project.RegistryUser, project.RegistryToken, ""); loginErr != nil {
		err := fmt.Errorf("registry login failed: %w", loginErr)
		dLogger.Log(err.Error())
		return err
	}
	dLogger.Log(fmt.Sprintf("Logged in to registry as %s", project.RegistryUser))

	// Build
	dLogger.Log("Building images...")
	buildLogs, buildErr := e.docker.ComposeBuild(workspaceDir, params.DeploymentFilename, overrideFilename)
	dLogger.LogBlock("BUILD LOGS", buildLogs)
	if buildErr != nil {
		return buildErr
	}

	// Push
	dLogger.Log("Pushing images...")
	pushLogs, pushErr := e.docker.ComposePush(workspaceDir, params.DeploymentFilename, overrideFilename)
	dLogger.LogBlock("PUSH LOGS", pushLogs)
	if pushErr != nil {
		return pushErr
	}

	return nil
}

// executeRemoteSSH handles the SSH connection and remote command execution
func (e *DeploymentExecutor) executeRemoteSSH(project *models.Project, params models.PipelineRunParams, workspaceDir, overrideFilename string, overrideContent []byte, dLogger *DeploymentLogger) error {
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
	db         *database.DB
	pipelineID int
	logs       strings.Builder
}

func (e *DeploymentExecutor) newDeploymentLogger(pipelineID int) *DeploymentLogger {
	return &DeploymentLogger{
		db:         e.db,
		pipelineID: pipelineID,
	}
}

func (dLogger *DeploymentLogger) Log(msg string) {
	// 1. Append to local builder (for return)
	dLogger.logs.WriteString(msg + "\n")

	// 2. Stream to DB
	if dLogger.db != nil && dLogger.pipelineID > 0 {
		if dbErr := dLogger.db.CreateDeploymentLog(dLogger.pipelineID, msg); dbErr != nil {
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

// sanitizeProjectName sanitizes the project name for Docker Compose
func sanitizeProjectName(name string) string {
	name = strings.ToLower(name)
	reg := regexp.MustCompile("[^a-z0-9]+")
	name = reg.ReplaceAllString(name, "-")
	return strings.Trim(name, "-")
}
