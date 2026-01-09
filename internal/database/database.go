package database

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Soif2Sang/imt-cloud-CI-CD-backend.git/internal/models"
	_ "github.com/lib/pq"
)

type DB struct {
	conn          *sql.DB
	encryptionKey string
}

func New(encryptionKey string) (*DB, error) {
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

	return &DB{
		conn:          conn,
		encryptionKey: encryptionKey,
	}, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) Encrypt(text string) (string, error) {
	if db.encryptionKey == "" {
		return text, nil
	}
	block, err := aes.NewCipher([]byte(db.encryptionKey))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(text), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (db *DB) Decrypt(text string) (string, error) {
	if db.encryptionKey == "" {
		return text, nil
	}
	data, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return text, nil // Return raw text if not base64 (migration support)
	}
	block, err := aes.NewCipher([]byte(db.encryptionKey))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return text, nil // Return raw text if too short
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return text, nil // Return raw text if decryption fails (wrong key or not encrypted)
	}
	return string(plaintext), nil
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

	encAccessToken, err := db.Encrypt(project.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt access token: %w", err)
	}
	encSSHPrivateKey, err := db.Encrypt(project.SSHPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt ssh key: %w", err)
	}
	encRegistryToken, err := db.Encrypt(project.RegistryToken)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt registry token: %w", err)
	}

	query := `
		INSERT INTO projects (owner_id, name, repo_url, access_token, pipeline_filename, deployment_filename, ssh_host, ssh_user, ssh_private_key, registry_user, registry_token)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, owner_id, name, repo_url, access_token, pipeline_filename, deployment_filename, ssh_host, ssh_user, ssh_private_key, registry_user, registry_token, created_at
	`
	var p models.Project
	err = db.conn.QueryRow(query, project.OwnerID, project.Name, project.RepoURL, encAccessToken, project.PipelineFilename, project.DeploymentFilename,
		project.SSHHost, project.SSHUser, encSSHPrivateKey, project.RegistryUser, encRegistryToken).
		Scan(&p.ID, &p.OwnerID, &p.Name, &p.RepoURL, &p.AccessToken, &p.PipelineFilename, &p.DeploymentFilename,
			&p.SSHHost, &p.SSHUser, &p.SSHPrivateKey, &p.RegistryUser, &p.RegistryToken, &p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create project: %w", err)
	}

	// Restore plaintext values in returned object
	p.AccessToken = project.AccessToken
	p.SSHPrivateKey = project.SSHPrivateKey
	p.RegistryToken = project.RegistryToken

	return &p, nil
}

// GetProject retrieves a project by ID
func (db *DB) GetProject(id int) (*models.Project, error) {
	query := `
		SELECT id, owner_id, name, repo_url, access_token, pipeline_filename, deployment_filename,
		COALESCE(ssh_host, ''), COALESCE(ssh_user, ''), COALESCE(ssh_private_key, ''),
		COALESCE(registry_user, ''), COALESCE(registry_token, ''),
		created_at
		FROM projects WHERE id = $1
	`
	var p models.Project
	err := db.conn.QueryRow(query, id).
		Scan(&p.ID, &p.OwnerID, &p.Name, &p.RepoURL, &p.AccessToken, &p.PipelineFilename, &p.DeploymentFilename,
			&p.SSHHost, &p.SSHUser, &p.SSHPrivateKey, &p.RegistryUser, &p.RegistryToken,
			&p.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("project not found")
		}
		return nil, fmt.Errorf("failed to get project: %w", err)
	}

	// Decrypt sensitive fields
	p.AccessToken, _ = db.Decrypt(p.AccessToken)
	p.SSHPrivateKey, _ = db.Decrypt(p.SSHPrivateKey)
	p.RegistryToken, _ = db.Decrypt(p.RegistryToken)

	variables, err := db.GetVariablesByProject(id)
	if err == nil {
		// Mask secrets
		for i := range variables {
			if variables[i].IsSecret {
				variables[i].Value = "*****"
			}
		}
		p.Variables = variables
	}

	return &p, nil
}

// GetAllProjects retrieves all projects
func (db *DB) GetAllProjects() ([]models.Project, error) {
	query := `
		SELECT id, owner_id, name, repo_url, access_token, pipeline_filename, deployment_filename,
		COALESCE(ssh_host, ''), COALESCE(ssh_user, ''), COALESCE(ssh_private_key, ''),
		COALESCE(registry_user, ''), COALESCE(registry_token, ''),
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
			&p.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan project: %w", err)
		}

		// Decrypt sensitive fields
		p.AccessToken, _ = db.Decrypt(p.AccessToken)
		p.SSHPrivateKey, _ = db.Decrypt(p.SSHPrivateKey)
		p.RegistryToken, _ = db.Decrypt(p.RegistryToken)

		projects = append(projects, p)
	}
	return projects, nil
}

// GetProjectsForUser retrieves projects where user is owner or member
func (db *DB) GetProjectsForUser(userID int) ([]models.Project, error) {
	query := `
		SELECT DISTINCT p.id, p.owner_id, p.name, p.repo_url, p.access_token, p.pipeline_filename, p.deployment_filename,
		COALESCE(p.ssh_host, ''), COALESCE(p.ssh_user, ''), COALESCE(p.ssh_private_key, ''),
		COALESCE(p.registry_user, ''), COALESCE(p.registry_token, ''),
		p.created_at
		FROM projects p
		LEFT JOIN project_members pm ON p.id = pm.project_id
		WHERE p.owner_id = $1 OR pm.user_id = $1
		ORDER BY p.created_at DESC
	`
	rows, err := db.conn.Query(query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to query projects: %w", err)
	}
	defer rows.Close()

	var projects []models.Project
	for rows.Next() {
		var p models.Project
		if err := rows.Scan(&p.ID, &p.OwnerID, &p.Name, &p.RepoURL, &p.AccessToken, &p.PipelineFilename, &p.DeploymentFilename,
			&p.SSHHost, &p.SSHUser, &p.SSHPrivateKey, &p.RegistryUser, &p.RegistryToken,
			&p.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan project: %w", err)
		}

		// Decrypt sensitive fields
		p.AccessToken, _ = db.Decrypt(p.AccessToken)
		p.SSHPrivateKey, _ = db.Decrypt(p.SSHPrivateKey)
		p.RegistryToken, _ = db.Decrypt(p.RegistryToken)

		projects = append(projects, p)
	}
	return projects, nil
}

func (db *DB) FindProjectByUrl(url string) (*models.Project, error) {
	query := `
		SELECT id, owner_id, name, repo_url, access_token, pipeline_filename, deployment_filename,
		COALESCE(ssh_host, ''), COALESCE(ssh_user, ''), COALESCE(ssh_private_key, ''),
		COALESCE(registry_user, ''), COALESCE(registry_token, ''),
		created_at
		FROM projects WHERE repo_url = $1
	`
	var p models.Project
	err := db.conn.QueryRow(query, url).
		Scan(&p.ID, &p.OwnerID, &p.Name, &p.RepoURL, &p.AccessToken, &p.PipelineFilename, &p.DeploymentFilename,
			&p.SSHHost, &p.SSHUser, &p.SSHPrivateKey, &p.RegistryUser, &p.RegistryToken,
			&p.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("project not found")
		}
		return nil, fmt.Errorf("failed to get project: %w", err)
	}

	// Decrypt sensitive fields
	p.AccessToken, _ = db.Decrypt(p.AccessToken)
	p.SSHPrivateKey, _ = db.Decrypt(p.SSHPrivateKey)
	p.RegistryToken, _ = db.Decrypt(p.RegistryToken)

	return &p, nil
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

	encAccessToken, err := db.Encrypt(project.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt access token: %w", err)
	}
	encSSHPrivateKey, err := db.Encrypt(project.SSHPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt ssh key: %w", err)
	}
	encRegistryToken, err := db.Encrypt(project.RegistryToken)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt registry token: %w", err)
	}

	query := `
		UPDATE projects
		SET name = $1, repo_url = $2, access_token = $3, pipeline_filename = $4, deployment_filename = $5,
		ssh_host = $6, ssh_user = $7, ssh_private_key = $8, registry_user = $9, registry_token = $10
		WHERE id = $11
		RETURNING id, name, repo_url, access_token, pipeline_filename, deployment_filename, ssh_host, ssh_user, ssh_private_key, registry_user, registry_token, created_at
	`
	var p models.Project
	err = db.conn.QueryRow(query, project.Name, project.RepoURL, encAccessToken, project.PipelineFilename, project.DeploymentFilename,
		project.SSHHost, project.SSHUser, encSSHPrivateKey, project.RegistryUser, encRegistryToken, id).
		Scan(&p.ID, &p.Name, &p.RepoURL, &p.AccessToken, &p.PipelineFilename, &p.DeploymentFilename,
			&p.SSHHost, &p.SSHUser, &p.SSHPrivateKey, &p.RegistryUser, &p.RegistryToken, &p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to update project: %w", err)
	}

	// Restore plaintext values in returned object
	p.AccessToken = project.AccessToken
	p.SSHPrivateKey = project.SSHPrivateKey
	p.RegistryToken = project.RegistryToken

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

// ============== Project Member Operations ==============

// AddProjectMember adds a user to a project
func (db *DB) AddProjectMember(projectID, userID int, role string) error {
	query := `
		INSERT INTO project_members (project_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (project_id, user_id) DO UPDATE SET role = EXCLUDED.role
	`
	_, err := db.conn.Exec(query, projectID, userID, role)
	if err != nil {
		return fmt.Errorf("failed to add project member: %w", err)
	}
	return nil
}

// GetProjectMembers retrieves all members of a project
func (db *DB) GetProjectMembers(projectID int) ([]models.ProjectMember, error) {
	query := `
		SELECT pm.project_id, pm.user_id, pm.role, pm.joined_at,
		       u.id, u.email, u.name, u.avatar_url
		FROM project_members pm
		JOIN users u ON pm.user_id = u.id
		WHERE pm.project_id = $1
		ORDER BY pm.joined_at DESC
	`
	rows, err := db.conn.Query(query, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to query project members: %w", err)
	}
	defer rows.Close()

	var members []models.ProjectMember
	for rows.Next() {
		var pm models.ProjectMember
		var u models.User
		if err := rows.Scan(&pm.ProjectID, &pm.UserID, &pm.Role, &pm.JoinedAt,
			&u.ID, &u.Email, &u.Name, &u.AvatarURL); err != nil {
			return nil, fmt.Errorf("failed to scan project member: %w", err)
		}
		pm.User = &u
		members = append(members, pm)
	}
	return members, nil
}

// RemoveProjectMember removes a user from a project
func (db *DB) RemoveProjectMember(projectID, userID int) error {
	query := `DELETE FROM project_members WHERE project_id = $1 AND user_id = $2`
	_, err := db.conn.Exec(query, projectID, userID)
	if err != nil {
		return fmt.Errorf("failed to remove project member: %w", err)
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
// GetLastSuccessfulPipeline retrieves the last successful pipeline for a project
func (db *DB) GetLastSuccessfulPipeline(projectID int) (*models.Pipeline, error) {
	query := `
		SELECT id, project_id, status, commit_hash, branch, created_at, finished_at
		FROM pipelines
		WHERE project_id = $1 AND status = 'success'
		ORDER BY id DESC
		LIMIT 1
	`
	var p models.Pipeline
	var finishedAt sql.NullTime
	err := db.conn.QueryRow(query, projectID).
		Scan(&p.ID, &p.ProjectID, &p.Status, &p.CommitHash, &p.Branch, &p.CreatedAt, &finishedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get last successful pipeline: %w", err)
	}
	if finishedAt.Valid {
		p.FinishedAt = &finishedAt.Time
	}
	return &p, nil
}

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

// GetJobByName retrieves a job by pipeline ID and name
func (db *DB) GetJobByName(pipelineID int, name string) (*models.Job, error) {
	query := `SELECT id, pipeline_id, name, stage, image, status, exit_code, started_at, finished_at FROM jobs WHERE pipeline_id = $1 AND name = $2`
	var j models.Job
	var exitCode sql.NullInt64
	var startedAt, finishedAt sql.NullTime
	err := db.conn.QueryRow(query, pipelineID, name).
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
	var startedAt time.Time
	err := db.conn.QueryRow(query, pipelineID).
		Scan(&d.ID, &d.PipelineID, &d.Status, &startedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment: %w", err)
	}
	d.StartedAt = &startedAt
	return &d, nil
}

// UpdateDeploymentStatus updates the status of a deployment
func (db *DB) UpdateDeploymentStatus(id int, status string) error {
	var query string
	if status == "success" || status == "failed" || status == "rolled_back" {
		query = `UPDATE deployments SET status = $1, finished_at = CURRENT_TIMESTAMP WHERE id = $2`
	} else if status == "deploying" {
		query = `UPDATE deployments SET status = $1, started_at = CURRENT_TIMESTAMP WHERE id = $2`
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
	var startedAt, finishedAt sql.NullTime
	err := db.conn.QueryRow(query, pipelineID).
		Scan(&d.ID, &d.PipelineID, &d.Status, &startedAt, &finishedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Return nil if no deployment found
		}
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}
	if startedAt.Valid {
		d.StartedAt = &startedAt.Time
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

func (db *DB) CreateVariable(v *models.Variable) error {
	encryptedValue, err := db.Encrypt(v.Value)
	if err != nil {
		return fmt.Errorf("failed to encrypt variable value: %w", err)
	}

	query := `
		INSERT INTO variables (project_id, key, value, is_secret)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at
	`
	return db.conn.QueryRow(query, v.ProjectID, v.Key, encryptedValue, v.IsSecret).Scan(&v.ID, &v.CreatedAt)
}

func (db *DB) GetVariablesByProject(projectID int) ([]models.Variable, error) {
	query := `
		SELECT id, project_id, key, value, is_secret, created_at
		FROM variables
		WHERE project_id = $1
	`
	rows, err := db.conn.Query(query, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to get variables: %w", err)
	}
	defer rows.Close()

	var variables []models.Variable
	for rows.Next() {
		var v models.Variable
		if err := rows.Scan(&v.ID, &v.ProjectID, &v.Key, &v.Value, &v.IsSecret, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan variable: %w", err)
		}

		decryptedValue, err := db.Decrypt(v.Value)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt variable value: %w", err)
		}
		v.Value = decryptedValue

		variables = append(variables, v)
	}
	return variables, nil
}

func (db *DB) DeleteVariable(projectID int, key string) error {
	query := `DELETE FROM variables WHERE project_id = $1 AND key = $2`
	_, err := db.conn.Exec(query, projectID, key)
	return err
}

func (db *DB) CreatePendingDeployment(pipelineID int) (*models.Deployment, error) {
	query := `
		INSERT INTO deployments (pipeline_id, status, started_at)
		VALUES ($1, 'pending', NULL)
		RETURNING id, status, started_at
	`
	var d models.Deployment
	var startedAt sql.NullTime
	err := db.conn.QueryRow(query, pipelineID).Scan(&d.ID, &d.Status, &startedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create pending deployment: %w", err)
	}
	if startedAt.Valid {
		d.StartedAt = &startedAt.Time
	}
	return &d, nil
}
