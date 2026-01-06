package database

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/lib/pq"

	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/models"
)

type DB struct {
	conn *sql.DB
}

func New() (*DB, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://cicd:cicd_password@localhost:5432/cicd_db?sslmode=disable"
	}

	conn, err := sql.Open("postgres", dbURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	conn.SetMaxOpenConns(25)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(5 * time.Minute)

	// Test connection
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &DB{conn: conn}, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}

// ============== User Operations ==============

func (db *DB) CreateUser(user *models.User) error {
	query := `
		INSERT INTO users (email, name, avatar_url, provider, provider_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (email) DO UPDATE SET
			name = EXCLUDED.name,
			avatar_url = EXCLUDED.avatar_url,
			provider = EXCLUDED.provider,
			provider_id = EXCLUDED.provider_id
		RETURNING id, created_at
	`
	return db.conn.QueryRow(query, user.Email, user.Name, user.AvatarURL, user.Provider, user.ProviderID).
		Scan(&user.ID, &user.CreatedAt)
}

func (db *DB) GetUserByEmail(email string) (*models.User, error) {
	var user models.User
	query := `SELECT id, email, name, avatar_url, provider, provider_id, created_at FROM users WHERE email = $1`
	err := db.conn.QueryRow(query, email).Scan(
		&user.ID, &user.Email, &user.Name, &user.AvatarURL, &user.Provider, &user.ProviderID, &user.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (db *DB) GetUserByID(id int) (*models.User, error) {
	var user models.User
	query := `SELECT id, email, name, avatar_url, provider, provider_id, created_at FROM users WHERE id = $1`
	err := db.conn.QueryRow(query, id).Scan(
		&user.ID, &user.Email, &user.Name, &user.AvatarURL, &user.Provider, &user.ProviderID, &user.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// ============== Project Operations ==============

// CreateProject creates a new project in the database
func (db *DB) CreateProject(project *models.NewProject) (*models.Project, error) {
	// Set defaults if empty
	if project.PipelineFilename == "" {
		project.PipelineFilename = "pipeline.yml"
	}
	if project.DeploymentFilename == "" {
		project.DeploymentFilename = "docker-compose.yml"
	}

	query := `
		INSERT INTO projects (owner_id, name, repo_url, access_token, pipeline_filename, deployment_filename, ssh_host, ssh_user, ssh_private_key, registry_user, registry_token, sonar_url, sonar_token)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, owner_id, name, repo_url, access_token, pipeline_filename, deployment_filename, ssh_host, ssh_user, ssh_private_key, registry_user, registry_token, sonar_url, sonar_token, created_at
	`
	var p models.Project
	err := db.conn.QueryRow(query, project.OwnerID, project.Name, project.RepoURL, project.AccessToken, project.PipelineFilename, project.DeploymentFilename,
		project.SSHHost, project.SSHUser, project.SSHPrivateKey, project.RegistryUser, project.RegistryToken, project.SonarURL, project.SonarToken).
		Scan(&p.ID, &p.OwnerID, &p.Name, &p.RepoURL, &p.AccessToken, &p.PipelineFilename, &p.DeploymentFilename,
			&p.SSHHost, &p.SSHUser, &p.SSHPrivateKey, &p.RegistryUser, &p.RegistryToken, &p.SonarURL, &p.SonarToken, &p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create project: %w", err)
	}
	return &p, nil
}

// GetProject retrieves a project by ID
func (db *DB) GetProject(id int) (*models.Project, error) {
	query := `
		SELECT id, owner_id, name, repo_url, access_token, pipeline_filename, deployment_filename,
		COALESCE(ssh_host, ''), COALESCE(ssh_user, ''), COALESCE(ssh_private_key, ''),
		COALESCE(registry_user, ''), COALESCE(registry_token, ''),
		COALESCE(sonar_url, ''), COALESCE(sonar_token, ''),
		created_at
		FROM projects WHERE id = $1
	`
	var p models.Project
	err := db.conn.QueryRow(query, id).
		Scan(&p.ID, &p.OwnerID, &p.Name, &p.RepoURL, &p.AccessToken, &p.PipelineFilename, &p.DeploymentFilename,
			&p.SSHHost, &p.SSHUser, &p.SSHPrivateKey, &p.RegistryUser, &p.RegistryToken,
			&p.SonarURL, &p.SonarToken,
			&p.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("project not found")
		}
		return nil, fmt.Errorf("failed to get project: %w", err)
	}
	return &p, nil
}

// GetAllProjects retrieves all projects
func (db *DB) GetAllProjects() ([]models.Project, error) {
	query := `
		SELECT id, owner_id, name, repo_url, access_token, pipeline_filename, deployment_filename,
		COALESCE(ssh_host, ''), COALESCE(ssh_user, ''), COALESCE(ssh_private_key, ''),
		COALESCE(registry_user, ''), COALESCE(registry_token, ''),
		COALESCE(sonar_url, ''), COALESCE(sonar_token, ''),
		created_at
		FROM projects ORDER BY created_at DESC
	`
	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query projects: %w", err)
	}
	defer rows.Close()

	var projects []models.Project
	for rows.Next() {
		var p models.Project
		if err := rows.Scan(&p.ID, &p.OwnerID, &p.Name, &p.RepoURL, &p.AccessToken, &p.PipelineFilename, &p.DeploymentFilename,
			&p.SSHHost, &p.SSHUser, &p.SSHPrivateKey, &p.RegistryUser, &p.RegistryToken,
			&p.SonarURL, &p.SonarToken,
			&p.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan project: %w", err)
		}
		projects = append(projects, p)
	}
	return projects, nil
}

// UpdateProject updates an existing project
func (db *DB) UpdateProject(id int, project *models.NewProject) (*models.Project, error) {
	// Set defaults if empty
	if project.PipelineFilename == "" {
		project.PipelineFilename = ".gitlab-ci.yml"
	}
	if project.DeploymentFilename == "" {
		project.DeploymentFilename = "docker-compose.yml"
	}

	query := `
		UPDATE projects
		SET name = $1, repo_url = $2, access_token = $3, pipeline_filename = $4, deployment_filename = $5,
		ssh_host = $6, ssh_user = $7, ssh_private_key = $8, registry_user = $9, registry_token = $10, sonar_url = $11, sonar_token = $12
		WHERE id = $13
		RETURNING id, name, repo_url, access_token, pipeline_filename, deployment_filename, ssh_host, ssh_user, ssh_private_key, registry_user, registry_token, sonar_url, sonar_token, created_at
	`
	var p models.Project
	err := db.conn.QueryRow(query, project.Name, project.RepoURL, project.AccessToken, project.PipelineFilename, project.DeploymentFilename,
		project.SSHHost, project.SSHUser, project.SSHPrivateKey, project.RegistryUser, project.RegistryToken, project.SonarURL, project.SonarToken, id).
		Scan(&p.ID, &p.Name, &p.RepoURL, &p.AccessToken, &p.PipelineFilename, &p.DeploymentFilename,
			&p.SSHHost, &p.SSHUser, &p.SSHPrivateKey, &p.RegistryUser, &p.RegistryToken, &p.SonarURL, &p.SonarToken, &p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to update project: %w", err)
	}
	return &p, nil
}

// DeleteProject deletes a project by ID
func (db *DB) DeleteProject(id int) error {
	query := `DELETE FROM projects WHERE id = $1`
	result, err := db.conn.Exec(query, id)
	if err != nil {
		return fmt.Errorf("failed to delete project: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("project not found")
	}
	return nil
}

// ============== Pipeline Operations ==============

// CreatePipeline creates a new pipeline in the database
func (db *DB) CreatePipeline(projectID int, branch, commitHash string) (*models.Pipeline, error) {
	query := `
		INSERT INTO pipelines (project_id, status, branch, commit_hash)
		VALUES ($1, 'pending', $2, $3)
		RETURNING id, project_id, status, commit_hash, branch, created_at, finished_at
	`
	var p models.Pipeline
	var finishedAt sql.NullTime
	err := db.conn.QueryRow(query, projectID, branch, commitHash).
		Scan(&p.ID, &p.ProjectID, &p.Status, &p.CommitHash, &p.Branch, &p.CreatedAt, &finishedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create pipeline: %w", err)
	}
	if finishedAt.Valid {
		p.FinishedAt = &finishedAt.Time
	}
	return &p, nil
}

// GetPipeline retrieves a pipeline by ID
func (db *DB) GetPipeline(id int) (*models.Pipeline, error) {
	query := `SELECT id, project_id, status, commit_hash, branch, created_at, finished_at FROM pipelines WHERE id = $1`
	var p models.Pipeline
	var finishedAt sql.NullTime
	var commitHash, branch sql.NullString
	err := db.conn.QueryRow(query, id).
		Scan(&p.ID, &p.ProjectID, &p.Status, &commitHash, &branch, &p.CreatedAt, &finishedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("pipeline not found")
		}
		return nil, fmt.Errorf("failed to get pipeline: %w", err)
	}
	if finishedAt.Valid {
		p.FinishedAt = &finishedAt.Time
	}
	if commitHash.Valid {
		p.CommitHash = commitHash.String
	}
	if branch.Valid {
		p.Branch = branch.String
	}
	return &p, nil
}

// GetPipelinesByProject retrieves all pipelines for a project
func (db *DB) GetPipelinesByProject(projectID int) ([]models.Pipeline, error) {
	query := `
		SELECT id, project_id, status, commit_hash, branch, created_at, finished_at
		FROM pipelines
		WHERE project_id = $1
		ORDER BY created_at DESC
	`
	rows, err := db.conn.Query(query, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to query pipelines: %w", err)
	}
	defer rows.Close()

	var pipelines []models.Pipeline
	for rows.Next() {
		var p models.Pipeline
		var finishedAt sql.NullTime
		var commitHash, branch sql.NullString
		if err := rows.Scan(&p.ID, &p.ProjectID, &p.Status, &commitHash, &branch, &p.CreatedAt, &finishedAt); err != nil {
			return nil, fmt.Errorf("failed to scan pipeline: %w", err)
		}
		if finishedAt.Valid {
			p.FinishedAt = &finishedAt.Time
		}
		if commitHash.Valid {
			p.CommitHash = commitHash.String
		}
		if branch.Valid {
			p.Branch = branch.String
		}
		pipelines = append(pipelines, p)
	}
	return pipelines, nil
}

// UpdatePipelineStatus updates the status of a pipeline
func (db *DB) UpdatePipelineStatus(id int, status string) error {
	var query string
	if status == "success" || status == "failed" || status == "cancelled" {
		query = `UPDATE pipelines SET status = $1, finished_at = CURRENT_TIMESTAMP WHERE id = $2`
	} else {
		query = `UPDATE pipelines SET status = $1 WHERE id = $2`
	}
	_, err := db.conn.Exec(query, status, id)
	if err != nil {
		return fmt.Errorf("failed to update pipeline status: %w", err)
	}
	return nil
}

// ============== Job Operations ==============

// CreateJob creates a new job in the database
func (db *DB) CreateJob(pipelineID int, name, stage, image string) (*models.Job, error) {
	query := `
		INSERT INTO jobs (pipeline_id, name, stage, image, status)
		VALUES ($1, $2, $3, $4, 'pending')
		RETURNING id, pipeline_id, name, stage, image, status, exit_code, started_at, finished_at
	`
	var j models.Job
	var exitCode sql.NullInt64
	var startedAt, finishedAt sql.NullTime
	err := db.conn.QueryRow(query, pipelineID, name, stage, image).
		Scan(&j.ID, &j.PipelineID, &j.Name, &j.Stage, &j.Image, &j.Status, &exitCode, &startedAt, &finishedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create job: %w", err)
	}
	if exitCode.Valid {
		j.ExitCode = int(exitCode.Int64)
	}
	if startedAt.Valid {
		j.StartedAt = &startedAt.Time
	}
	if finishedAt.Valid {
		j.FinishedAt = &finishedAt.Time
	}
	return &j, nil
}

// GetJob retrieves a job by ID
func (db *DB) GetJob(id int) (*models.Job, error) {
	query := `SELECT id, pipeline_id, name, stage, image, status, exit_code, started_at, finished_at FROM jobs WHERE id = $1`
	var j models.Job
	var exitCode sql.NullInt64
	var startedAt, finishedAt sql.NullTime
	err := db.conn.QueryRow(query, id).
		Scan(&j.ID, &j.PipelineID, &j.Name, &j.Stage, &j.Image, &j.Status, &exitCode, &startedAt, &finishedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("job not found")
		}
		return nil, fmt.Errorf("failed to get job: %w", err)
	}
	if exitCode.Valid {
		j.ExitCode = int(exitCode.Int64)
	}
	if startedAt.Valid {
		j.StartedAt = &startedAt.Time
	}
	if finishedAt.Valid {
		j.FinishedAt = &finishedAt.Time
	}
	return &j, nil
}

// GetJobsByPipeline retrieves all jobs for a pipeline
func (db *DB) GetJobsByPipeline(pipelineID int) ([]models.Job, error) {
	query := `
		SELECT id, pipeline_id, name, stage, image, status, exit_code, started_at, finished_at
		FROM jobs
		WHERE pipeline_id = $1
		ORDER BY id ASC
	`
	rows, err := db.conn.Query(query, pipelineID)
	if err != nil {
		return nil, fmt.Errorf("failed to query jobs: %w", err)
	}
	defer rows.Close()

	var jobs []models.Job
	for rows.Next() {
		var j models.Job
		var exitCode sql.NullInt64
		var startedAt, finishedAt sql.NullTime
		if err := rows.Scan(&j.ID, &j.PipelineID, &j.Name, &j.Stage, &j.Image, &j.Status, &exitCode, &startedAt, &finishedAt); err != nil {
			return nil, fmt.Errorf("failed to scan job: %w", err)
		}
		if exitCode.Valid {
			j.ExitCode = int(exitCode.Int64)
		}
		if startedAt.Valid {
			j.StartedAt = &startedAt.Time
		}
		if finishedAt.Valid {
			j.FinishedAt = &finishedAt.Time
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

// UpdateJobStatus updates the status of a job
func (db *DB) UpdateJobStatus(id int, status string, exitCode *int) error {
	var query string
	var args []interface{}

	if status == "running" {
		query = `UPDATE jobs SET status = $1, started_at = CURRENT_TIMESTAMP WHERE id = $2`
		args = []interface{}{status, id}
	} else if status == "success" || status == "failed" {
		query = `UPDATE jobs SET status = $1, exit_code = $2, finished_at = CURRENT_TIMESTAMP WHERE id = $3`
		var ec int
		if exitCode != nil {
			ec = *exitCode
		}
		args = []interface{}{status, ec, id}
	} else {
		query = `UPDATE jobs SET status = $1 WHERE id = $2`
		args = []interface{}{status, id}
	}

	_, err := db.conn.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to update job status: %w", err)
	}
	return nil
}

// ============== Log Operations ==============

// CreateLog creates a new log entry for a job
func (db *DB) CreateLog(jobID int, content string) (*models.LogLine, error) {
	query := `
		INSERT INTO job_logs (job_id, content)
		VALUES ($1, $2)
		RETURNING id, job_id, content, created_at
	`
	var l models.LogLine
	err := db.conn.QueryRow(query, jobID, content).
		Scan(&l.ID, &l.JobID, &l.Content, &l.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create log: %w", err)
	}
	return &l, nil
}

// CreateLogBatch creates multiple log entries for a job in a single transaction
func (db *DB) CreateLogBatch(jobID int, contents []string) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO job_logs (job_id, content) VALUES ($1, $2)`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, content := range contents {
		_, err := stmt.Exec(jobID, content)
		if err != nil {
			return fmt.Errorf("failed to insert log: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// GetLogsByJob retrieves all logs for a job
func (db *DB) GetLogsByJob(jobID int) ([]models.LogLine, error) {
	query := `
		SELECT id, job_id, content, created_at
		FROM job_logs
		WHERE job_id = $1
		ORDER BY created_at ASC, id ASC
	`
	rows, err := db.conn.Query(query, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to query logs: %w", err)
	}
	defer rows.Close()

	var logs []models.LogLine
	for rows.Next() {
		var l models.LogLine
		if err := rows.Scan(&l.ID, &l.JobID, &l.Content, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan log: %w", err)
		}
		logs = append(logs, l)
	}
	return logs, nil
}

// GetLogsSince retrieves logs for a job since a given timestamp (for streaming)
func (db *DB) GetLogsSince(jobID int, since time.Time) ([]models.LogLine, error) {
	query := `
		SELECT id, job_id, content, created_at
		FROM job_logs
		WHERE job_id = $1 AND created_at > $2
		ORDER BY created_at ASC, id ASC
	`
	rows, err := db.conn.Query(query, jobID, since)
	if err != nil {
		return nil, fmt.Errorf("failed to query logs: %w", err)
	}
	defer rows.Close()

	var logs []models.LogLine
	for rows.Next() {
		var l models.LogLine
		if err := rows.Scan(&l.ID, &l.JobID, &l.Content, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan log: %w", err)
		}
		logs = append(logs, l)
	}
	return logs, nil
}

// ============== Deployment Operations ==============

// CreateDeployment creates a new deployment in the database
func (db *DB) CreateDeployment(pipelineID int) (*models.Deployment, error) {
	query := `
		INSERT INTO deployments (pipeline_id, status)
		VALUES ($1, 'deploying')
		RETURNING id, pipeline_id, status, started_at
	`
	var d models.Deployment
	err := db.conn.QueryRow(query, pipelineID).
		Scan(&d.ID, &d.PipelineID, &d.Status, &d.StartedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment: %w", err)
	}
	return &d, nil
}

// UpdateDeploymentStatus updates the status of a deployment
func (db *DB) UpdateDeploymentStatus(id int, status string) error {
	var query string
	if status == "success" || status == "failed" || status == "rolled_back" {
		query = `UPDATE deployments SET status = $1, finished_at = CURRENT_TIMESTAMP WHERE id = $2`
	} else {
		query = `UPDATE deployments SET status = $1 WHERE id = $2`
	}
	_, err := db.conn.Exec(query, status, id)
	if err != nil {
		return fmt.Errorf("failed to update deployment status: %w", err)
	}
	return nil
}

// GetDeploymentByPipeline retrieves the deployment for a pipeline
func (db *DB) GetDeploymentByPipeline(pipelineID int) (*models.Deployment, error) {
	query := `SELECT id, pipeline_id, status, started_at, finished_at FROM deployments WHERE pipeline_id = $1`
	var d models.Deployment
	var finishedAt sql.NullTime
	err := db.conn.QueryRow(query, pipelineID).
		Scan(&d.ID, &d.PipelineID, &d.Status, &d.StartedAt, &finishedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Return nil if no deployment found
		}
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}
	if finishedAt.Valid {
		d.FinishedAt = &finishedAt.Time
	}
	return &d, nil
}

// CreateDeploymentLog creates a new log entry for a deployment
func (db *DB) CreateDeploymentLog(pipelineID int, content string) error {
	query := `INSERT INTO deployment_logs (pipeline_id, content) VALUES ($1, $2)`
	_, err := db.conn.Exec(query, pipelineID, content)
	if err != nil {
		return fmt.Errorf("failed to create deployment log: %w", err)
	}
	return nil
}

// GetDeploymentLogs retrieves all logs for a deployment (via pipeline_id)
func (db *DB) GetDeploymentLogs(pipelineID int) ([]models.DeploymentLog, error) {
	query := `
		SELECT id, pipeline_id, content, created_at
		FROM deployment_logs
		WHERE pipeline_id = $1
		ORDER BY created_at ASC, id ASC
	`
	rows, err := db.conn.Query(query, pipelineID)
	if err != nil {
		return nil, fmt.Errorf("failed to query deployment logs: %w", err)
	}
	defer rows.Close()

	var logs []models.DeploymentLog
	for rows.Next() {
		var l models.DeploymentLog
		if err := rows.Scan(&l.ID, &l.PipelineID, &l.Content, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan deployment log: %w", err)
		}
		logs = append(logs, l)
	}
	return logs, nil
}