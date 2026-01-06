package models

import "time"

type User struct {
	ID         int       `json:"id"`
	Email      string    `json:"email"`
	Name       string    `json:"name"`
	AvatarURL  string    `json:"avatar_url"`
	Provider   string    `json:"provider"`
	ProviderID string    `json:"provider_id"`
	CreatedAt  time.Time `json:"created_at"`
}

type Project struct {
	ID        int       `json:"id"`
	OwnerID   int       `json:"owner_id"`
	Name      string    `json:"name"`
	RepoURL            string    `json:"repo_url"`
	AccessToken        string    `json:"access_token"`
	PipelineFilename   string    `json:"pipeline_filename"`
	DeploymentFilename string    `json:"deployment_filename"`
	SSHHost            string    `json:"ssh_host"`
	SSHUser            string    `json:"ssh_user"`
	SSHPrivateKey      string    `json:"ssh_private_key"`
	RegistryUser       string    `json:"registry_user"`
	RegistryToken   string    `json:"registry_token"`
	SonarURL        string    `json:"sonar_url"`
	SonarToken      string    `json:"sonar_token"`
	CreatedAt          time.Time `json:"created_at"`
}

type NewProject struct {
	OwnerID            int    `json:"owner_id"`
	Name               string `json:"name"`
	RepoURL            string `json:"repo_url"`
	AccessToken        string `json:"access_token"`
	PipelineFilename   string `json:"pipeline_filename"`
	DeploymentFilename string `json:"deployment_filename"`
	SSHHost            string `json:"ssh_host"`
	SSHUser            string `json:"ssh_user"`
	SSHPrivateKey      string `json:"ssh_private_key"`
	RegistryUser       string `json:"registry_user"`
	RegistryToken   string `json:"registry_token"`
	SonarURL        string `json:"sonar_url"`
	SonarToken      string `json:"sonar_token"`
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

type Deployment struct {
	ID         int        `json:"id"`
	PipelineID int        `json:"pipeline_id"`
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type DeploymentLog struct {
	ID         int       `json:"id"`
	PipelineID int       `json:"pipeline_id"`
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"created_at"`
}

// PipelineRunParams contains parameters to run a pipeline
type PipelineRunParams struct {
	RepoURL            string
	RepoName           string
	Branch             string
	CommitHash         string
	AccessToken        string
	PipelineFilename   string
	DeploymentFilename string
	SSHHost            string
	SSHUser            string
	SSHPrivateKey      string
	RegistryUser       string
	RegistryToken   string
	SonarURL        string
	SonarToken      string
	ProjectID          int
	PipelineID         int
}

// PushEvent represents a GitHub push webhook payload
type PushEvent struct {
	Ref        string     `json:"ref"`
	Before     string     `json:"before"`
	After      string     `json:"after"`
	Created    bool       `json:"created"`
	Deleted    bool       `json:"deleted"`
	Forced     bool       `json:"forced"`
	Repository Repository `json:"repository"`
	Pusher     Pusher     `json:"pusher"`
	Sender     Sender     `json:"sender"`
	HeadCommit Commit     `json:"head_commit"`
	Commits    []Commit   `json:"commits"`
}

// Repository represents the repository information in the webhook
type Repository struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	CloneURL      string `json:"clone_url"`
	SSHURL        string `json:"ssh_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

// Pusher represents who pushed the commits
type Pusher struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Sender represents the GitHub user who triggered the event
type Sender struct {
	Login string `json:"login"`
	ID    int    `json:"id"`
}

// Commit represents a commit in the push event
type Commit struct {
	ID        string       `json:"id"`
	Message   string       `json:"message"`
	Timestamp string       `json:"timestamp"`
	Author    CommitAuthor `json:"author"`
	URL       string       `json:"url"`
	Added     []string     `json:"added"`
	Removed   []string     `json:"removed"`
	Modified  []string     `json:"modified"`
}

// CommitAuthor represents the author of a commit
type CommitAuthor struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Username string `json:"username"`
}
