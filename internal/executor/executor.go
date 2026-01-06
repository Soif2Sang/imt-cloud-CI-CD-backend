package executor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
)

type DockerExecutor struct {
	cli        *client.Client
	ctx        context.Context
	authConfig string
}

func NewDockerExecutor() (*DockerExecutor, error) {
	// Initialise le client en utilisant les variables d'environnement locales
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &DockerExecutor{
		cli: cli,
		ctx: context.Background(),
	}, nil
}

func (e *DockerExecutor) PullImage(imageName string) error {
	reader, err := e.cli.ImagePull(e.ctx, imageName, image.PullOptions{})
	if err != nil {
		return err
	}
	defer reader.Close()
	// On lit le flux jusqu'au bout pour attendre la fin du pull
	_, err = io.Copy(io.Discard, reader)
	return err
}

func (e *DockerExecutor) Login(username, password, serverAddress string) error {
	authConfig := registry.AuthConfig{
		Username:      username,
		Password:      password,
		ServerAddress: serverAddress,
	}

	encodedJSON, err := json.Marshal(authConfig)
	if err != nil {
		return err
	}

	authStr := base64.URLEncoding.EncodeToString(encodedJSON)

	_, err = e.cli.RegistryLogin(e.ctx, authConfig)
	if err != nil {
		return err
	}

	e.authConfig = authStr

	// Also login via CLI for docker compose commands
	cmd := exec.Command("docker", "login", "-u", username, "--password-stdin", serverAddress)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	go func() {
		defer stdin.Close()
		io.WriteString(stdin, password)
	}()

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker cli login failed: %s - %w", string(out), err)
	}

	return nil
}

func (e *DockerExecutor) PushImage(imageName string) error {
	opts := image.PushOptions{}
	if e.authConfig != "" {
		opts.RegistryAuth = e.authConfig
	}

	reader, err := e.cli.ImagePush(e.ctx, imageName, opts)
	if err != nil {
		return err
	}
	defer reader.Close()
	_, err = io.Copy(io.Discard, reader)
	return err
}

// ComposeBuild builds the services defined in docker-compose.yml
func (e *DockerExecutor) ComposeBuild(workDir, composeFile, overrideFile string) (string, error) {
	args := []string{"compose", "-f", composeFile}
	if overrideFile != "" {
		args = append(args, "-f", overrideFile)
	}
	args = append(args, "build")

	cmd := exec.Command("docker", args...)
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// ComposePush pushes the services defined in docker-compose.yml
func (e *DockerExecutor) ComposePush(workDir, composeFile, overrideFile string) (string, error) {
	args := []string{"compose", "-f", composeFile}
	if overrideFile != "" {
		args = append(args, "-f", overrideFile)
	}
	args = append(args, "push")

	cmd := exec.Command("docker", args...)
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// RunJobWithVolume runs a job with a workspace directory mounted into the container
func (e *DockerExecutor) RunJobWithVolume(imageName string, commands []string, workspacePath string) (string, error) {
	// On concatène les commandes avec " && " pour qu'elles s'exécutent séquentiellement
	cmdString := strings.Join(commands, " && ")

	// Configuration du conteneur
	containerConfig := &container.Config{
		Image:      imageName,
		Cmd:        []string{"sh", "-c", cmdString},
		WorkingDir: "/workspace",
	}

	// Configuration de l'hôte avec le volume monté
	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: workspacePath,        // Chemin sur l'hôte
				Target: "/workspace",         // Chemin dans le conteneur
			},
		},
	}

	// Créer le conteneur
	resp, err := e.cli.ContainerCreate(e.ctx, containerConfig, hostConfig, nil, nil, "")
	if err != nil {
		return "", err
	}

	// Démarrer le conteneur
	err = e.cli.ContainerStart(e.ctx, resp.ID, container.StartOptions{})
	return resp.ID, err
}

func (e *DockerExecutor) GetLogs(containerID string) (io.ReadCloser, error) {
	return e.cli.ContainerLogs(e.ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true, // Important pour le temps réel
	})
}

func (e *DockerExecutor) WaitForContainer(containerID string) (int64, error) {
	statusCh, errCh := e.cli.ContainerWait(e.ctx, containerID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		return 0, err
	case status := <-statusCh:
		return status.StatusCode, nil
	}
}

// RemoveContainer removes a container (cleanup)
func (e *DockerExecutor) RemoveContainer(containerID string) error {
	return e.cli.ContainerRemove(e.ctx, containerID, container.RemoveOptions{
		Force: true,
	})
}

// DeployCompose deploys using docker-compose with rollback capability
func (e *DockerExecutor) DeployCompose(workDir, composeFile, projectName string) (string, error) {
	var logs strings.Builder
	ctx := e.ctx

	baseArgs := []string{"compose"}
	if projectName != "" {
		baseArgs = append(baseArgs, "-p", projectName)
	}
	baseArgs = append(baseArgs, "-f", composeFile)

	// 1. Snapshot: Identify currently running containers and tag their images
	// We check ALL containers in the stack to ensure full rollback capability
	cmdPs := exec.Command("docker", append(baseArgs, "ps", "-q")...)
	cmdPs.Dir = workDir
	output, err := cmdPs.Output()

	backupImages := make(map[string]string) // ImageName -> ImageID

	if err == nil && len(output) > 0 {
		containerIDs := strings.Split(strings.TrimSpace(string(output)), "\n")
		for _, cid := range containerIDs {
			if cid == "" { continue }
			info, err := e.cli.ContainerInspect(ctx, cid)
			if err != nil {
				continue
			}

			imageName := info.Config.Image // e.g. "my-app:latest"
			imageID := info.Image          // e.g. "sha256:..."

			if imageName != "" {
				backupImages[imageName] = imageID
				// Create a backup tag
				backupTag := imageName + "-rollback"
				// Ignore error if tag already exists or fails, we try our best
				e.cli.ImageTag(ctx, imageID, backupTag)
			}
		}
	}

	// Helper for rollback
	performRollback := func() {
		if len(backupImages) == 0 {
			logs.WriteString("No backup available for rollback.\n")
			fmt.Println("No backup available for rollback.")
			return
		}
		logs.WriteString("Performing rollback...\n")
		fmt.Println("Performing rollback...")

		// Restore tags
		for name, id := range backupImages {
			// Force tag the old ID back to the original name
			if err := e.cli.ImageTag(ctx, id, name); err != nil {
				msg := fmt.Sprintf("Error restoring tag %s: %v\n", name, err)
				logs.WriteString(msg)
				fmt.Printf(msg)
			}
		}

		// Force recreate containers to use the restored tags
		argsRollback := append(baseArgs, "up", "-d", "--force-recreate")
		cmdRollback := exec.Command("docker", argsRollback...)
		cmdRollback.Dir = workDir
		if out, err := cmdRollback.CombinedOutput(); err != nil {
			msg := fmt.Sprintf("Rollback failed: %s\n", string(out))
			logs.WriteString(msg)
			fmt.Printf(msg)
		} else {
			logs.WriteString("Rollback successful.\n")
			fmt.Println("Rollback successful.")
		}
	}

	// 2. Pull
	argsPull := append(baseArgs, "pull")
	cmdPull := exec.Command("docker", argsPull...)
	cmdPull.Dir = workDir
	output, err = cmdPull.CombinedOutput()
	logs.Write(output)
	if err != nil {
		return logs.String(), fmt.Errorf("docker compose pull failed: %s: %w", string(output), err)
	}

	// 3. Up
	argsUp := append(baseArgs, "up", "-d", "--build")
	cmdUp := exec.Command("docker", argsUp...)
	cmdUp.Dir = workDir
	output, err = cmdUp.CombinedOutput()
	logs.Write(output)
	if err != nil {
		// Attempt to resolve container name conflicts automatically
		outStr := string(output)
		if strings.Contains(outStr, "Conflict") && strings.Contains(outStr, "already in use by container") {
			// Extract container ID from error message: "... already in use by container \"<id>\"..."
			parts := strings.Split(outStr, "already in use by container \"")
			if len(parts) > 1 {
				idParts := strings.Split(parts[1], "\"")
				if len(idParts) > 0 {
					conflictID := idParts[0]
					fmt.Printf("Detected name conflict with container %s. Removing and retrying...\n", conflictID)
					// Force remove the conflicting container
					_ = e.cli.ContainerRemove(ctx, conflictID, container.RemoveOptions{Force: true})

					// Retry docker compose up
					cmdUpRetry := exec.Command("docker", argsUp...)
					cmdUpRetry.Dir = workDir
					outputRetry, errRetry := cmdUpRetry.CombinedOutput()
					logs.Write(outputRetry)
					if errRetry == nil {
						err = nil // Retry succeeded
					} else {
						output = outputRetry
						err = errRetry
					}
				}
			}
		}

		if err != nil {
			performRollback()
			return logs.String(), fmt.Errorf("docker compose up failed: %s: %w", string(output), err)
		}
	}

	// 4. Health Check
	time.Sleep(10 * time.Second)

	// Check if ALL containers in the stack are running
	cmdHealth := exec.Command("docker", append(baseArgs, "ps", "-q")...)
	cmdHealth.Dir = workDir
	outHealth, err := cmdHealth.Output()
	if err != nil {
		performRollback()
		return logs.String(), fmt.Errorf("health check failed (ps error): %v", err)
	}

	cids := strings.Split(strings.TrimSpace(string(outHealth)), "\n")
	allRunning := true
	for _, cid := range cids {
		if cid == "" { continue }
		info, err := e.cli.ContainerInspect(ctx, cid)
		if err != nil {
			allRunning = false
			break
		}
		if !info.State.Running || info.State.Restarting {
			allRunning = false
			msg := fmt.Sprintf("Container %s is not running (State: %s)\n", info.Name, info.State.Status)
			logs.WriteString(msg)
			fmt.Printf(msg)
			break
		}
	}

	if !allRunning {
		performRollback()
		return logs.String(), fmt.Errorf("deployment failed: one or more services are unhealthy")
	}

	// 5. Cleanup Backups
	for name := range backupImages {
		e.cli.ImageRemove(ctx, name+"-rollback", image.RemoveOptions{})
	}

	return logs.String(), nil
}
