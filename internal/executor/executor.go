package executor

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
)

type DockerExecutor struct {
	cli *client.Client
	ctx context.Context
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

// RunJobWithVolume runs a job with a workspace directory mounted into the container
// It also mounts the Docker socket to allow Docker-out-of-Docker (DooD)
func (e *DockerExecutor) RunJobWithVolume(imageName string, commands []string, workspacePath string, envVars []string) (string, error) {
	// On concatène les commandes avec " && " pour qu'elles s'exécutent séquentiellement
	cmdString := strings.Join(commands, " && ")

	// Configuration du conteneur
	containerConfig := &container.Config{
		Image:      imageName,
		Cmd:        []string{"sh", "-c", cmdString},
		WorkingDir: "/workspace", // Le répertoire de travail dans le conteneur
		Env:        envVars,      // Injection des variables d'environnement
	}

	// Configuration de l'hôte avec le volume monté
	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: workspacePath,        // Chemin sur l'hôte
				Target: "/workspace",         // Chemin dans le conteneur
			},
			// Montage du socket Docker pour permettre le Docker-out-of-Docker (DooD)
			// Cela permet au conteneur de lancer des commandes docker (build, run, push)
			{
				Type:   mount.TypeBind,
				Source: "/var/run/docker.sock",
				Target: "/var/run/docker.sock",
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
func (e *DockerExecutor) DeployCompose(workDir, composeFile, projectName, serviceName string) error {
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
			fmt.Println("No backup available for rollback.")
			return
		}
		fmt.Println("Performing rollback...")

		// Restore tags
		for name, id := range backupImages {
			// Force tag the old ID back to the original name
			if err := e.cli.ImageTag(ctx, id, name); err != nil {
				fmt.Printf("Error restoring tag %s: %v\n", name, err)
			}
		}

		// Force recreate containers to use the restored tags
		argsRollback := append(baseArgs, "up", "-d", "--force-recreate")
		cmdRollback := exec.Command("docker", argsRollback...)
		cmdRollback.Dir = workDir
		if out, err := cmdRollback.CombinedOutput(); err != nil {
			fmt.Printf("Rollback failed: %s\n", string(out))
		} else {
			fmt.Println("Rollback successful.")
		}
	}

	// 2. Pull
	argsPull := append(baseArgs, "pull")
	if serviceName != "" {
		argsPull = append(argsPull, serviceName)
	}
	cmdPull := exec.Command("docker", argsPull...)
	cmdPull.Dir = workDir
	if output, err := cmdPull.CombinedOutput(); err != nil {
		return fmt.Errorf("docker compose pull failed: %s: %w", string(output), err)
	}

	// 3. Up
	argsUp := append(baseArgs, "up", "-d", "--build")
	if serviceName != "" {
		argsUp = append(argsUp, serviceName)
	}
	cmdUp := exec.Command("docker", argsUp...)
	cmdUp.Dir = workDir
	output, err = cmdUp.CombinedOutput()
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
			return fmt.Errorf("docker compose up failed: %s: %w", string(output), err)
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
		return fmt.Errorf("health check failed (ps error): %v", err)
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
			fmt.Printf("Container %s is not running (State: %s)\n", info.Name, info.State.Status)
			break
		}
	}

	if !allRunning {
		performRollback()
		return fmt.Errorf("deployment failed: one or more services are unhealthy")
	}

	// 5. Cleanup Backups
	for name := range backupImages {
		e.cli.ImageRemove(ctx, name+"-rollback", image.RemoveOptions{})
	}

	return nil
}
