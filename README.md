# IMT Cloud CI/CD Backend

A lightweight CI/CD engine built in Go that executes pipelines using Docker containers.

## Features

- 🔒 **OAuth2 Authentication** - Secure login with Google and GitHub
- 👤 **User Management** - Project ownership and role-based access
- 🔗 **GitHub Webhook Integration** - Automatically trigger pipelines on push
- 📄 **GitLab CI Config Parser** - Parse `.gitlab-ci.yml` configuration files
- 🐳 **Docker Execution** - Run jobs in isolated Docker containers
- 💾 **PostgreSQL Storage** - Store pipelines, jobs, and logs
- 📊 **Database Viewer** - Adminer for easy data inspection

## Architecture

```
GitHub Push → Webhook → Clone Repo → Parse .gitlab-ci.yml → Run Jobs in Docker → Store Logs
```

**Setup:**
- PostgreSQL + Adminer → Run in Docker
- Go backend → Run locally (avoids Docker-in-Docker issues)

## Prerequisites

- Go 1.21+
- Docker & Docker Compose
- Git

## Quick Start

### 1. Start the database

```bash
docker-compose up -d
```

This starts:
- **cicd-db** - PostgreSQL database on port `5432`
- **cicd-adminer** - Database viewer on port `8081`

### 2. Build and run the Go backend

```bash
# Install dependencies
go mod tidy

# Build
go build -o cicd-backend .

# Run
export DATABASE_URL="postgres://cicd:cicd_password@localhost:5432/cicd_db?sslmode=disable"
./cicd-backend
```

Or run directly:

```bash
DATABASE_URL="postgres://cicd:cicd_password@localhost:5432/cicd_db?sslmode=disable" go run .
```

### 3. Access the services

| Service | URL | Description |
|---------|-----|-------------|
| Backend API | http://localhost:8080 | CI/CD engine |
| Webhook Endpoint | http://localhost:8080/webhook/github | GitHub webhook receiver |
| Health Check | http://localhost:8080/health | Server status |
| Adminer | http://localhost:8081 | Database viewer |

**Adminer credentials:**
- System: `PostgreSQL`
- Server: `localhost`
- Username: `cicd`
- Password: `cicd_password`
- Database: `cicd_db`

## GitHub Webhook Setup

### 1. Expose your backend (for local development)

Use [ngrok](https://ngrok.com/) to expose your local server:

```bash
ngrok http 8080
```

This gives you a public URL like `https://abc123.ngrok.io`

### 2. Configure GitHub Webhook

1. Go to your GitHub repository → **Settings** → **Webhooks** → **Add webhook**
2. Configure:
   - **Payload URL**: `https://abc123.ngrok.io/webhook/github`
   - **Content type**: `application/json`
   - **Events**: Select "Just the push event"
3. Click **Add webhook**

### 3. Add a `.gitlab-ci.yml` to your repo

```yaml
stages:
  - build
  - test

build_job:
  stage: build
  image: alpine:latest
  script:
    - echo "Building the project..."
    - ls -la

test_job:
  stage: test
  image: alpine:latest
  script:
    - echo "Running tests..."
    - echo "All tests passed!"
```

### 4. Push code and watch the magic!

```bash
git add .
git commit -m "Add CI config"
git push
```

The backend will:
1. Receive the webhook from GitHub
2. Clone your repository
3. Parse the `.gitlab-ci.yml`
4. Execute each job in Docker containers
5. Store all logs in the database

## Configuration

### OAuth2 Setup

To enable authentication, you need to create OAuth applications in:
1. **Google Cloud Console**: Enable Google+ API, create credentials for Web Application.
   - Authorized redirect URI: `http://localhost:8080/auth/google/callback` (or your production URL)
2. **GitHub Developer Settings**: Create New OAuth App.
   - Authorization callback URL: `http://localhost:8080/auth/github/callback` (or your production URL)

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string | `postgres://cicd:cicd_password@localhost:5432/cicd_db?sslmode=disable` |
| `API_PORT` | Server port | `8080` |
| `API_URL` | Public URL of the backend (for callbacks) | `http://localhost:8080` |
| `FRONTEND_URL` | URL of the frontend (for redirects) | `http://localhost:3000` |
| `JWT_SECRET` | Secret key for signing JWT tokens | `your-secret-key...` |
| `GOOGLE_CLIENT_ID` | Google OAuth Client ID | - |
| `GOOGLE_CLIENT_SECRET` | Google OAuth Client Secret | - |
| `GITHUB_CLIENT_ID` | GitHub OAuth Client ID | - |
| `GITHUB_CLIENT_SECRET` | GitHub OAuth Client Secret | - |

## API Endpoints

### Authentication

```
GET /auth/google/login
GET /auth/github/login
```
Redirects to the respective provider for login.

### Webhook

```
POST /webhook/github
```

Receives GitHub push events and triggers pipelines.

**Headers:**
- `X-GitHub-Event: push`

**Response:**
```json
{
  "message": "Pipeline triggered",
  "branch": "main",
  "commit": "abc123..."
}
```

### Health Check

```
GET /health
```

**Response:**
```json
{
  "status": "ok"
}
```

## Database Schema

| Table | Description |
|-------|-------------|
| `projects` | Repository configurations |
| `pipelines` | Pipeline execution records |
| `jobs` | Individual job records within pipelines |
| `logs` | Line-by-line log storage |

## Supported CI Config

The parser supports a subset of GitLab CI syntax:

```yaml
stages:
  - build
  - test
  - deploy

job_name:
  stage: build
  image: node:18-alpine
  script:
    - npm install
    - npm run build
```

## Project Structure

```
.
├── main.go                 # Entry point - starts API server
├── internal/
│   ├── api/
│   │   ├── server.go       # HTTP server and handlers
│   │   └── webhook.go      # GitHub webhook payload types
│   ├── database/
│   │   └── database.go     # PostgreSQL operations
│   ├── executor/
│   │   └── executor.go     # Docker job execution
│   ├── git/
│   │   └── git.go          # Git clone operations
│   ├── models/
│   │   └── models.go       # Data models
│   └── parser/
│       └── parser.go       # YAML config parser
├── docker-compose.yml      # PostgreSQL + Adminer
└── init-db.sql             # Database schema
```

## Stopping the services

```bash
# Stop database
docker-compose down

# Stop with data cleanup
docker-compose down -v
```

## Testing the webhook manually

```bash
curl -X POST http://localhost:8080/webhook/github \
  -H "Content-Type: application/json" \
  -H "X-GitHub-Event: push" \
  -d '{
    "ref": "refs/heads/main",
    "after": "abc123def456",
    "deleted": false,
    "repository": {
      "name": "test-repo",
      "full_name": "user/test-repo",
      "clone_url": "https://github.com/user/test-repo.git"
    },
    "head_commit": {
      "id": "abc123def456",
      "message": "test commit"
    }
  }'
```

## License

MIT