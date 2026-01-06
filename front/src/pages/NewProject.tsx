import React, { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useMutation } from '@tanstack/react-query';
import { createProject, NewProject as NewProjectType } from '../lib/api';
import { Save, Loader2, AlertCircle, ArrowLeft } from 'lucide-react';
import { Link } from 'react-router-dom';

export function NewProject() {
  const navigate = useNavigate();
  const [error, setError] = useState<string | null>(null);

  const [formData, setFormData] = useState<NewProjectType>({
    name: '',
    repo_url: '',
    access_token: '',
    pipeline_filename: 'pipeline.yml',
    deployment_filename: 'docker-compose.yml',
    ssh_host: '',
    ssh_user: '',
    ssh_private_key: '',
    registry_image: '',
    registry_user: '',
    registry_token: ''
  });

  const mutation = useMutation({
    mutationFn: createProject,
    onSuccess: (data) => {
      navigate(`/projects/${data.id}`);
    },
    onError: (err: any) => {
      setError(err.response?.data?.error || 'Failed to create project');
    }
  });

  const handleChange = (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => {
    const { name, value } = e.target;
    setFormData(prev => ({ ...prev, [name]: value }));
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    mutation.mutate(formData);
  };

  return (
    <div className="max-w-3xl mx-auto">
      <div className="mb-6">
        <Link to="/" className="inline-flex items-center text-sm text-gray-500 hover:text-gray-900 mb-2">
          <ArrowLeft size={16} className="mr-1" />
          Back to Projects
        </Link>
        <h1 className="text-2xl font-bold text-gray-900">Create New Project</h1>
        <p className="text-gray-500 mt-1">Connect a repository to start building pipelines</p>
      </div>

      {error && (
        <div className="mb-6 bg-red-50 border border-red-200 text-red-700 px-4 py-3 rounded-lg flex items-center gap-2">
          <AlertCircle size={20} />
          {error}
        </div>
      )}

      <form onSubmit={handleSubmit} className="space-y-6">
        {/* Basic Info */}
        <div className="bg-white border border-gray-200 rounded-xl p-6 shadow-sm">
          <h2 className="text-lg font-semibold text-gray-900 mb-4 pb-2 border-b border-gray-100">
            Repository Details
          </h2>
          <div className="space-y-4">
            <div>
              <label htmlFor="name" className="block text-sm font-medium text-gray-700 mb-1">
                Project Name <span className="text-red-500">*</span>
              </label>
              <input
                type="text"
                id="name"
                name="name"
                required
                value={formData.name}
                onChange={handleChange}
                placeholder="My Awesome Project"
                className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              />
            </div>

            <div>
              <label htmlFor="repo_url" className="block text-sm font-medium text-gray-700 mb-1">
                Git Repository URL <span className="text-red-500">*</span>
              </label>
              <input
                type="url"
                id="repo_url"
                name="repo_url"
                required
                value={formData.repo_url}
                onChange={handleChange}
                placeholder="https://github.com/username/repo.git"
                className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              />
            </div>

            <div>
              <label htmlFor="access_token" className="block text-sm font-medium text-gray-700 mb-1">
                Personal Access Token
              </label>
              <input
                type="password"
                id="access_token"
                name="access_token"
                value={formData.access_token}
                onChange={handleChange}
                placeholder="ghp_..."
                className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              />
              <p className="mt-1 text-xs text-gray-500">Required for private repositories (GitHub PAT, GitLab Token, etc.)</p>
            </div>
          </div>
        </div>

        {/* SSH Config */}
        <div className="bg-white border border-gray-200 rounded-xl p-6 shadow-sm">
          <h2 className="text-lg font-semibold text-gray-900 mb-4 pb-2 border-b border-gray-100">
            SSH Deployment Configuration
          </h2>
          <div className="space-y-4">
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div>
                <label htmlFor="ssh_host" className="block text-sm font-medium text-gray-700 mb-1">
                  SSH Host (IP:Port)
                </label>
                <input
                  type="text"
                  id="ssh_host"
                  name="ssh_host"
                  value={formData.ssh_host}
                  onChange={handleChange}
                  placeholder="192.168.1.10:22"
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                />
              </div>
              <div>
                <label htmlFor="ssh_user" className="block text-sm font-medium text-gray-700 mb-1">
                  SSH Username
                </label>
                <input
                  type="text"
                  id="ssh_user"
                  name="ssh_user"
                  value={formData.ssh_user}
                  onChange={handleChange}
                  placeholder="ubuntu"
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                />
              </div>
            </div>

            <div>
              <label htmlFor="ssh_private_key" className="block text-sm font-medium text-gray-700 mb-1">
                SSH Private Key
              </label>
              <textarea
                id="ssh_private_key"
                name="ssh_private_key"
                rows={5}
                value={formData.ssh_private_key}
                onChange={handleChange}
                placeholder="-----BEGIN OPENSSH PRIVATE KEY-----..."
                className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent font-mono text-xs"
              />
              <p className="mt-1 text-xs text-gray-500">The private key used to connect to the deployment server.</p>
            </div>
          </div>
        </div>

        {/* Registry Config */}
        <div className="bg-white border border-gray-200 rounded-xl p-6 shadow-sm">
          <h2 className="text-lg font-semibold text-gray-900 mb-4 pb-2 border-b border-gray-100">
            Docker Registry
          </h2>
          <div className="space-y-4">
            <div>
              <label htmlFor="registry_image" className="block text-sm font-medium text-gray-700 mb-1">
                Image Name
              </label>
              <input
                type="text"
                id="registry_image"
                name="registry_image"
                value={formData.registry_image}
                onChange={handleChange}
                placeholder="username/image-name"
                className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              />
            </div>

            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div>
                <label htmlFor="registry_user" className="block text-sm font-medium text-gray-700 mb-1">
                  Registry Username
                </label>
                <input
                  type="text"
                  id="registry_user"
                  name="registry_user"
                  value={formData.registry_user}
                  onChange={handleChange}
                  placeholder="dockerhub-user"
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                />
              </div>
              <div>
                <label htmlFor="registry_token" className="block text-sm font-medium text-gray-700 mb-1">
                  Registry Token / Password
                </label>
                <input
                  type="password"
                  id="registry_token"
                  name="registry_token"
                  value={formData.registry_token}
                  onChange={handleChange}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                />
              </div>
            </div>
          </div>
        </div>

        {/* Configuration Files */}
        <div className="bg-white border border-gray-200 rounded-xl p-6 shadow-sm">
          <h2 className="text-lg font-semibold text-gray-900 mb-4 pb-2 border-b border-gray-100">
            Configuration Files
          </h2>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <label htmlFor="pipeline_filename" className="block text-sm font-medium text-gray-700 mb-1">
                Pipeline Filename
              </label>
              <input
                type="text"
                id="pipeline_filename"
                name="pipeline_filename"
                value={formData.pipeline_filename}
                onChange={handleChange}
                placeholder="pipeline.yml"
                className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent font-mono"
              />
            </div>
            <div>
              <label htmlFor="deployment_filename" className="block text-sm font-medium text-gray-700 mb-1">
                Deployment Filename
              </label>
              <input
                type="text"
                id="deployment_filename"
                name="deployment_filename"
                value={formData.deployment_filename}
                onChange={handleChange}
                placeholder="docker-compose.yml"
                className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent font-mono"
              />
            </div>
          </div>
        </div>

        <div className="flex justify-end pt-4">
          <button
            type="submit"
            disabled={mutation.isPending}
            className="flex items-center gap-2 bg-blue-600 hover:bg-blue-700 disabled:bg-blue-400 text-white px-6 py-2.5 rounded-lg font-medium transition-colors shadow-sm"
          >
            {mutation.isPending ? (
              <Loader2 size={20} className="animate-spin" />
            ) : (
              <Save size={20} />
            )}
            Create Project
          </button>
        </div>
      </form>
    </div>
  );
}