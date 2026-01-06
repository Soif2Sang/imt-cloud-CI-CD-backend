package pipeline

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type PipelineConfig struct {
	Stages []string             `yaml:"stages"`
	Jobs   map[string]JobConfig `yaml:",inline"`
}

type JobConfig struct {
	Stage      string            `yaml:"stage"`
	Image      string            `yaml:"image"`
	Script     []string          `yaml:"script"`
	Type       string            `yaml:"type,omitempty"`       // shell (default), docker-deploy, docker-compose-deploy
	Properties map[string]string `yaml:"properties,omitempty"` // Params spécifiques au type de job
}

type Parser struct {
	FilePath string
}

func NewParser(filePath string) *Parser {
	return &Parser{FilePath: filePath}
}

func (p *Parser) Parse() (*PipelineConfig, error) {
	data, err := os.ReadFile(p.FilePath)
	if err != nil {
		return nil, fmt.Errorf("impossible de lire le fichier : %w", err)
	}

	var config PipelineConfig
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, fmt.Errorf("erreur lors du décodage YAML : %w", err)
	}

	return &config, nil
}