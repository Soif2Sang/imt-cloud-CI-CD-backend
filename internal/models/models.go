package models

import "time"

type Project struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	RepoURL            string    `json:"repo_url"`
	AccessToken        string    `json:"access_token"`
	PipelineFilename   string    `json:"pipeline_filename"`
	DeploymentFilename string    `json:"deployment_filename"`
	CreatedAt          time.Time `json:"created_at"`
}

type NewProject struct {
	Name               string `json:"name"`
	RepoURL            string `json:"repo_url"`
	AccessToken        string `json:"access_token"`
	PipelineFilename   string `json:"pipeline_filename"`
	DeploymentFilename string `json:"deployment_filename"`
}

type Pipeline struct {
	ID         int        `json:"id"`
	ProjectID  int        `json:"project_id"`
	Status     string     `json:"status"`
	CommitHash string     `json:"commit_hash,omitempty"`
	Branch     string     `json:"branch,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type Job struct {
	ID         int        `json:"id"`
	PipelineID int        `json:"pipeline_id"`
	Name       string     `json:"name"`
	Stage      string     `json:"stage"`
	Image      string     `json:"image"`
	Status     string     `json:"status"`
	ExitCode   int        `json:"exit_code"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type LogLine struct {
	ID        int       `json:"id"`
	JobID     int       `json:"job_id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}
