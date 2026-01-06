package compose

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ComposeConfig represents the partial structure of a docker-compose file
// We only care about the keys under 'services'
type ComposeConfig struct {
	Services map[string]interface{} `yaml:"services"`
}

// ParseServices reads a docker-compose file and returns the list of service names
func ParseServices(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read compose file: %w", err)
	}

	var config ComposeConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse compose file: %w", err)
	}

	var services []string
	for name := range config.Services {
		services = append(services, name)
	}

	return services, nil
}

// GenerateOverride creates the YAML content for docker-compose.override.yml
// It enforces standardized image names for all services based on the project, registry and commit hash.
// Format: registryUser/project-service:tag
func GenerateOverride(services []string, registryUser, projectName, tag string) ([]byte, error) {
	serviceConfig := make(map[string]interface{})

	cleanProject := strings.ToLower(strings.ReplaceAll(projectName, " ", "-"))

	for _, service := range services {
		cleanService := strings.ToLower(strings.ReplaceAll(service, " ", "-"))

		// Construct standardized image name
		// e.g. "myuser/myproject-backend:abc1234"
		imageName := fmt.Sprintf("%s/%s-%s:%s", registryUser, cleanProject, cleanService, tag)

		// We only override the 'image' field
		serviceConfig[service] = map[string]string{
			"image": imageName,
		}
	}

	override := map[string]interface{}{
		"services": serviceConfig,
	}

	return yaml.Marshal(override)
}
