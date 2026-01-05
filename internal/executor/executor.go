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
	"github.com/docker/go-connections/nat"
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

func (e *DockerExecutor) RunJob(imageName string, commands []string) (string, error) {
	// On concatène les commandes avec " && " pour qu'elles s'exécutent séquentiellement
	cmdString := strings.Join(commands, " && ")

	// 1. Configurer le conteneur avec vos commandes
	resp, err := e.cli.ContainerCreate(e.ctx, &container.Config{
		Image:      imageName,
		Cmd:        []string{"sh", "-c", cmdString}, // On encapsule dans un shell
		WorkingDir: "/workspace",
	}, nil, nil, nil, "")
	if err != nil {
		return "", err
	}

	// 2. Démarrer le conteneur
	err = e.cli.ContainerStart(e.ctx, resp.ID, container.StartOptions{})
	return resp.ID, err
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

// DeploySingleContainer deploys a single container with rollback capability
func (e *DockerExecutor) DeploySingleContainer(imageName, containerName, portMapping string) error {
	ctx := e.ctx

	// 1. Check if container exists and prepare backup
	oldContainer, err := e.cli.ContainerInspect(ctx, containerName)
	var backupImageID string
	hasOld := err == nil

	if hasOld {
		backupImageID = oldContainer.Image
		// Tag current image as backup
		if err := e.cli.ImageTag(ctx, backupImageID, imageName+":rollback-backup"); err != nil {
			return fmt.Errorf("failed to tag backup: %w", err)
		}
		// Remove old container
		if err := e.cli.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true}); err != nil {
			return fmt.Errorf("failed to remove old container: %w", err)
		}
	}

	// 2. Start new container
	if err := e.startContainer(imageName, containerName, portMapping); err != nil {
		// Rollback if start failed
		if hasOld {
			fmt.Printf("Deployment failed, rolling back to %s...\n", backupImageID)
			e.startContainer(backupImageID, containerName, portMapping)
		}
		return fmt.Errorf("failed to start new container: %w", err)
	}

	// 3. Health Check
	time.Sleep(5 * time.Second) // Wait for startup
	inspect, err := e.cli.ContainerInspect(ctx, containerName)
	if err != nil || !inspect.State.Running || inspect.State.Restarting {
		// Rollback if health check failed
		if hasOld {
			fmt.Printf("Health check failed, rolling back to %s...\n", backupImageID)
			e.cli.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})
			e.startContainer(backupImageID, containerName, portMapping)
		}
		return fmt.Errorf("deployment failed health check")
	}

	// 4. Cleanup backup
	if hasOld {
		e.cli.ImageRemove(ctx, imageName+":rollback-backup", image.RemoveOptions{})
	}

	return nil
}

// startContainer is a helper to create and start a container
func (e *DockerExecutor) startContainer(imageID, containerName, portMapping string) error {
	ctx := e.ctx

	// Configure ports
	portBindings := nat.PortMap{}
	exposedPorts := nat.PortSet{}
	if portMapping != "" {
		parts := strings.Split(portMapping, ":")
		if len(parts) == 2 {
			hostPort := parts[0]
			containerPort := parts[1]
			p := nat.Port(containerPort + "/tcp")
			exposedPorts[p] = struct{}{}
			portBindings[p] = []nat.PortBinding{
				{
					HostIP:   "0.0.0.0",
					HostPort: hostPort,
				},
			}
		}
	}

	// Create container
	resp, err := e.cli.ContainerCreate(ctx,
		&container.Config{
			Image:        imageID,
			ExposedPorts: exposedPorts,
		},
		&container.HostConfig{
			PortBindings: portBindings,
		},
		nil, nil, containerName)
	if err != nil {
		return err
	}

	// Start container
	return e.cli.ContainerStart(ctx, resp.ID, container.StartOptions{})
}

// DeployCompose deploys using docker-compose with rollback capability
func (e *DockerExecutor) DeployCompose(workDir, composeFile, serviceName string) error {
	ctx := e.ctx

	// 1. Snapshot: Identify currently running containers and tag their images
	// We check ALL containers in the stack to ensure full rollback capability
	cmdPs := exec.Command("docker-compose", "-f", composeFile, "ps", "-q")
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
		argsRollback := []string{"-f", composeFile, "up", "-d", "--force-recreate"}
		cmdRollback := exec.Command("docker-compose", argsRollback...)
		cmdRollback.Dir = workDir
		if out, err := cmdRollback.CombinedOutput(); err != nil {
			fmt.Printf("Rollback failed: %s\n", string(out))
		} else {
			fmt.Println("Rollback successful.")
		}
	}

	// 2. Pull
	argsPull := []string{"-f", composeFile, "pull"}
	if serviceName != "" {
		argsPull = append(argsPull, serviceName)
	}
	cmdPull := exec.Command("docker-compose", argsPull...)
	cmdPull.Dir = workDir
	if output, err := cmdPull.CombinedOutput(); err != nil {
		return fmt.Errorf("docker-compose pull failed: %s: %w", string(output), err)
	}

	// 3. Up
	argsUp := []string{"-f", composeFile, "up", "-d"}
	if serviceName != "" {
		argsUp = append(argsUp, serviceName)
	}
	cmdUp := exec.Command("docker-compose", argsUp...)
	cmdUp.Dir = workDir
	if output, err := cmdUp.CombinedOutput(); err != nil {
		performRollback()
		return fmt.Errorf("docker-compose up failed: %s: %w", string(output), err)
	}

	// 4. Health Check
	time.Sleep(10 * time.Second)

	// Check if ALL containers in the stack are running
	cmdHealth := exec.Command("docker-compose", "-f", composeFile, "ps", "-q")
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