# IMT Cloud CI/CD Backend

A lightweight, robust CI/CD engine built in Go. It supports custom pipelines, Docker-based execution, automated deployments via SSH, and automatic rollbacks.

## 🚀 Getting Started

### Prerequisites

- **Docker** & **Docker Compose**
- **Go** 1.21+
- **Node.js** & **npm** (for the frontend)

### Installation & Startup

1.  **Start Infrastructure**
    Launch the database and SonarQube services:
    ```bash
    docker-compose up -d
    ```

2.  **Configure & Run Backend**
    ```bash
    # Install dependencies
    go mod tidy

    # Configure environment
    cp .env.example .env
    # Populate the .env file (Database connection, OAuth credentials, etc.)

    # Run the server
    go run main.go
    ```
    The backend API will be available at `http://localhost:8080`.

3.  **Run Frontend** (in a separate terminal)
    Navigate to the frontend directory:
    ```bash
    cd ../imt-cloud-CI-CD-frontend
    npm install
    npm run dev
    ```
    Access the UI at `http://localhost:5173`.

---

## 🛠 Usage Workflow

### 1. Create a Project
1.  Log in to the platform.
2.  Click **"New Project"**.
3.  Provide the **Repository URL** (HTTPS).
4.  (Optional) Provide a **Personal Access Token** if the repo is private.

### 2. Configure Deployment (SSH)
To enable automated deployment, you must set up SSH access to your target server.

**⚠️ Prerequisite: Setup Target Host**
Before configuring the project, ensure your deployment server is ready:
1.  Ensure you have a user (e.g., `ubuntu` or `root`) that can run `docker` commands without sudo.
2.  Generate an SSH key pair (or use an existing one).
3.  Add the **Public Key** to the target user's `~/.ssh/authorized_keys` file.

**In the CI/CD Project Settings:**
1.  Go to **Project Settings**.
2.  **SSH Host**: Enter the IP address and port (e.g., `192.168.1.10:22`).
3.  **SSH User**: Enter the username (e.g., `ubuntu`).
4.  **SSH Private Key**: Paste the **Private Key** content directly.

### 3. Configure Container Registry
To push built images to a registry (Docker Hub, etc.):
1.  In **Project Settings** > **Container Registry**.
2.  Enter **Registry User** (e.g., Docker Hub username).
3.  Enter **Registry Token** (Access Token).

### 4. Environment Variables
You can inject secrets (like `SONAR_TOKEN`, `API_KEYS`) without hardcoding them in your files:
1.  Go to **Project Settings** > **Environment Variables**.
2.  Add Key/Value pairs.
3.  Toggle the **Lock Icon** to mark sensitive values as **Secret**.
4.  These are injected into your pipeline jobs automatically.

---

## 📄 Pipeline Configuration

Add a `pipeline.yml` (default name) to your repository root. We use a lightweight, GitLab-CI inspired syntax.

```yaml
stages:
  - build
  - test
  - scan

build_job:
  stage: build
  image: python:3.9
  script:
    - pip install -r requirements.txt
    - python setup.py build
```

## 🐳 Deployment Configuration

Add a `docker-compose.yml` to your repository root.
The system automatically handles versioning by generating a `docker-compose.override.yml` that points to the specific image tag built in the pipeline.

**Automatic Rollback:**
If a deployment fails (e.g., a container crashes immediately after startup), the system detects the failure and **automatically rolls back** to the last known successful commit.

**Conflict Handling:**
The deployment engine automatically handles container name conflicts by cleaning up old containers before starting the new version, ensuring a smooth update process.

---

## 📚 Documentation

For detailed technical architecture and internal workings, please refer to [TECHNICAL_DOCS.md](TECHNICAL_DOCS.md).