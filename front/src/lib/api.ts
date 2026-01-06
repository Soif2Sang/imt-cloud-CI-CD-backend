import axios from 'axios';

const API_URL = 'http://localhost:8080/api/v1';

export const api = axios.create({
  baseURL: API_URL,
  headers: {
    'Content-Type': 'application/json',
  },
});

export interface Project {
  id: number;
  name: string;
  repo_url: string;
  access_token: string;
  created_at: string;
  ssh_host?: string;
  ssh_user?: string;
  ssh_private_key?: string;
  registry_image?: string;
  registry_user?: string;
  registry_token?: string;
  pipeline_filename?: string;
  deployment_filename?: string;
}

export interface NewProject {
  name: string;
  repo_url: string;
  access_token?: string;
  ssh_host?: string;
  ssh_user?: string;
  ssh_private_key?: string;
  registry_image?: string;
  registry_user?: string;
  registry_token?: string;
  pipeline_filename?: string;
  deployment_filename?: string;
}

export interface Pipeline {
  id: number;
  project_id: number;
  status: 'pending' | 'running' | 'success' | 'failed';
  commit_hash: string;
  branch: string;
  created_at: string;
  finished_at?: string;
}

export interface Job {
  id: number;
  pipeline_id: number;
  name: string;
  stage: string;
  image: string;
  status: 'pending' | 'running' | 'success' | 'failed';
  exit_code?: number;
  started_at?: string;
  finished_at?: string;
}

export interface Log {
  id: number;
  job_id: number;
  content: string;
  created_at: string;
}

export interface Deployment {
  id: number;
  pipeline_id: number;
  status: 'deploying' | 'success' | 'failed' | 'rolled_back';
  started_at: string;
  finished_at?: string;
}

export interface DeploymentLog {
  id: number;
  pipeline_id: number;
  content: string;
  created_at: string;
}

// Projects
export const getProjects = async () => {
  const { data } = await api.get<Project[]>('/projects');
  return data;
};

export const getProject = async (id: number) => {
  const { data } = await api.get<Project>(`/projects/${id}`);
  return data;
};

export const createProject = async (project: NewProject) => {
  const { data } = await api.post<Project>('/projects', project);
  return data;
};

export const deleteProject = async (id: number) => {
  await api.delete(`/projects/${id}`);
};

// Pipelines
export const getPipelines = async (projectId: number) => {
  const { data } = await api.get<Pipeline[]>(`/projects/${projectId}/pipelines`);
  return data;
};

export const getPipeline = async (projectId: number, pipelineId: number) => {
  const { data } = await api.get<Pipeline>(`/projects/${projectId}/pipelines/${pipelineId}`);
  return data;
};

export const triggerPipeline = async (projectId: number, branch: string = 'main') => {
  const { data } = await api.post<Pipeline>(`/projects/${projectId}/pipelines`, { branch });
  return data;
};

// Jobs
export const getJobs = async (projectId: number, pipelineId: number) => {
  const { data } = await api.get<Job[]>(`/projects/${projectId}/pipelines/${pipelineId}/jobs`);
  return data;
};

export const getJob = async (projectId: number, pipelineId: number, jobId: number) => {
  const { data } = await api.get<Job>(`/projects/${projectId}/pipelines/${pipelineId}/jobs/${jobId}`);
  return data;
};

export const getJobLogs = async (projectId: number, pipelineId: number, jobId: number) => {
  const { data } = await api.get<Log[]>(`/projects/${projectId}/pipelines/${pipelineId}/jobs/${jobId}/logs`);
  return data;
};

// Deployments
export const getDeployment = async (projectId: number, pipelineId: number) => {
  try {
    const { data } = await api.get<Deployment>(`/projects/${projectId}/pipelines/${pipelineId}/deployment`);
    return data;
  } catch (error) {
    if (axios.isAxiosError(error) && error.response?.status === 404) {
      return null;
    }
    throw error;
  }
};

export const getDeploymentLogs = async (projectId: number, pipelineId: number) => {
  const { data } = await api.get<DeploymentLog[]>(`/projects/${projectId}/pipelines/${pipelineId}/deployment/logs`);
  return data;
};