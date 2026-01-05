-- PostgreSQL initialization script for CI/CD backend

-- Table des projets (Repositories)
CREATE TABLE IF NOT EXISTS projects (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    repo_url TEXT NOT NULL UNIQUE,
    access_token TEXT NOT NULL,
    pipeline_filename TEXT DEFAULT 'pipeline.yml',
    deployment_filename TEXT DEFAULT 'docker-compose.yml',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Table des pipelines (Une exécution du fichier .gitlab-ci.yml)
CREATE TABLE IF NOT EXISTS pipelines (
    id SERIAL PRIMARY KEY,
    project_id INTEGER NOT NULL,
    status TEXT DEFAULT 'pending', -- pending, running, success, failed, cancelled
    commit_hash TEXT,              -- Le hash du commit qui a déclenché la pipeline
    branch TEXT,                   -- La branche concernée (ex: main)
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMP,
    FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- Table des jobs (Les tâches individuelles dans une pipeline)
CREATE TABLE IF NOT EXISTS jobs (
    id SERIAL PRIMARY KEY,
    pipeline_id INTEGER NOT NULL,
    name TEXT NOT NULL,            -- ex: build_job
    stage TEXT NOT NULL,           -- ex: build, test
    image TEXT NOT NULL,           -- ex: alpine:latest
    status TEXT DEFAULT 'pending', -- pending, running, success, failed
    exit_code INTEGER,             -- Code de retour du conteneur (0 = succès)
    started_at TIMESTAMP,
    finished_at TIMESTAMP,
    FOREIGN KEY(pipeline_id) REFERENCES pipelines(id) ON DELETE CASCADE
);

-- Table des logs (Stockage unitaire ligne par ligne pour le streaming)
CREATE TABLE IF NOT EXISTS logs (
    id SERIAL PRIMARY KEY,
    job_id INTEGER NOT NULL,
    content TEXT,                  -- Le contenu de la ligne de log
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, -- Pour trier les logs dans l'ordre
    FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE
);

-- Index pour optimiser les requêtes fréquentes
CREATE INDEX IF NOT EXISTS idx_pipelines_project_id ON pipelines(project_id);
CREATE INDEX IF NOT EXISTS idx_pipelines_status ON pipelines(status);
CREATE INDEX IF NOT EXISTS idx_jobs_pipeline_id ON jobs(pipeline_id);
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
CREATE INDEX IF NOT EXISTS idx_logs_job_id ON logs(job_id); -- Crucial pour récupérer les logs rapidement
CREATE INDEX IF NOT EXISTS idx_logs_created_at ON logs(created_at);