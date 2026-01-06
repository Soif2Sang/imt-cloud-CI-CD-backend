import React from 'react';
import { useParams, Link } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { getProject, getPipelines, triggerPipeline } from '../lib/api';
import { 
  GitCommit, 
  GitBranch, 
  Clock, 
  Play, 
  CheckCircle2, 
  XCircle, 
  Loader2, 
  AlertCircle,
  ExternalLink,
  Box
} from 'lucide-react';
import { formatDistanceToNow } from 'date-fns';
import { clsx } from 'clsx';

const StatusBadge = ({ status }: { status: string }) => {
  const styles = {
    success: "bg-green-50 text-green-700 border-green-200",
    failed: "bg-red-50 text-red-700 border-red-200",
    running: "bg-blue-50 text-blue-700 border-blue-200",
    pending: "bg-gray-50 text-gray-700 border-gray-200",
  };

  const icons = {
    success: <CheckCircle2 size={14} />,
    failed: <XCircle size={14} />,
    running: <Loader2 size={14} className="animate-spin" />,
    pending: <Clock size={14} />,
  };

  const s = status as keyof typeof styles;

  return (
    <span className={clsx(
      "inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium border",
      styles[s] || styles.pending
    )}>
      {icons[s] || icons.pending}
      <span className="capitalize">{status}</span>
    </span>
  );
};

export function ProjectDetail() {
  const { id } = useParams<{ id: string }>();
  const projectId = parseInt(id || '0');
  const queryClient = useQueryClient();

  const { data: project, isLoading: isProjectLoading } = useQuery({
    queryKey: ['project', projectId],
    queryFn: () => getProject(projectId),
    enabled: !!projectId,
  });

  const { data: pipelines, isLoading: isPipelinesLoading } = useQuery({
    queryKey: ['pipelines', projectId],
    queryFn: () => getPipelines(projectId),
    enabled: !!projectId,
    refetchInterval: 5000, // Poll every 5 seconds for updates
  });

  const triggerMutation = useMutation({
    mutationFn: () => triggerPipeline(projectId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['pipelines', projectId] });
    },
  });

  if (isProjectLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-600"></div>
      </div>
    );
  }

  if (!project) {
    return (
      <div className="bg-red-50 border border-red-200 text-red-700 px-4 py-3 rounded-lg flex items-center gap-2">
        <AlertCircle size={20} />
        Project not found
      </div>
    );
  }

  return (
    <div className="space-y-8">
      {/* Header */}
      <div className="bg-white border border-gray-200 rounded-xl p-6">
        <div className="flex flex-col md:flex-row md:items-start md:justify-between gap-4">
          <div>
            <div className="flex items-center gap-3">
              <h1 className="text-2xl font-bold text-gray-900">{project.name}</h1>
              <span className="bg-gray-100 text-gray-600 text-xs px-2 py-1 rounded font-mono">
                ID: {project.id}
              </span>
            </div>
            <a 
              href={project.repo_url} 
              target="_blank" 
              rel="noreferrer"
              className="flex items-center gap-2 text-gray-500 mt-2 hover:text-blue-600 transition-colors"
            >
              <ExternalLink size={16} />
              {project.repo_url}
            </a>
          </div>
          
          <button
            onClick={() => triggerMutation.mutate()}
            disabled={triggerMutation.isPending}
            className="flex items-center gap-2 bg-blue-600 hover:bg-blue-700 disabled:bg-blue-400 text-white px-4 py-2 rounded-lg font-medium transition-colors"
          >
            {triggerMutation.isPending ? (
              <Loader2 size={18} className="animate-spin" />
            ) : (
              <Play size={18} />
            )}
            Run Pipeline
          </button>
        </div>
        
        {/* Project Stats / Info */}
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mt-6 pt-6 border-t border-gray-100">
            <div className="flex items-center gap-3 text-sm text-gray-600">
                <div className="p-2 bg-gray-50 rounded-lg"><GitBranch size={16} /></div>
                <div>
                    <p className="text-gray-500 text-xs">Default Branch</p>
                    <p className="font-medium">main</p>
                </div>
            </div>
            <div className="flex items-center gap-3 text-sm text-gray-600">
                <div className="p-2 bg-gray-50 rounded-lg"><Box size={16} /></div>
                <div>
                    <p className="text-gray-500 text-xs">Docker Registry</p>
                    <p className="font-medium">{project.registry_image || 'Not configured'}</p>
                </div>
            </div>
            {/* Add more stats here if needed */}
        </div>
      </div>

      {/* Pipelines List */}
      <div>
        <h2 className="text-lg font-bold text-gray-900 mb-4 flex items-center gap-2">
            <Clock size={20} className="text-gray-500"/>
            Pipeline History
        </h2>
        
        <div className="bg-white border border-gray-200 rounded-xl overflow-hidden">
            {isPipelinesLoading ? (
                 <div className="p-8 text-center text-gray-500">Loading pipelines...</div>
            ) : pipelines?.length === 0 ? (
                <div className="p-12 text-center">
                    <div className="mx-auto h-12 w-12 text-gray-300 mb-3">
                        <Play size={48} strokeWidth={1} />
                    </div>
                    <h3 className="text-gray-900 font-medium">No pipelines run yet</h3>
                    <p className="text-gray-500 text-sm mt-1">Trigger your first pipeline to get started</p>
                </div>
            ) : (
                <div className="divide-y divide-gray-100">
                    {pipelines?.map((pipeline) => (
                        <Link 
                            key={pipeline.id} 
                            to={`/projects/${project.id}/pipelines/${pipeline.id}`}
                            className="block p-4 hover:bg-gray-50 transition-colors"
                        >
                            <div className="flex items-center justify-between">
                                <div className="flex items-center gap-4">
                                    <StatusBadge status={pipeline.status} />
                                    
                                    <div>
                                        <div className="flex items-center gap-3 text-sm text-gray-900 font-medium">
                                            <span>#{pipeline.id}</span>
                                            <span className="text-gray-300">|</span>
                                            <span className="flex items-center gap-1">
                                                <GitCommit size={14} className="text-gray-400" />
                                                <span className="font-mono text-xs">
                                                  {pipeline.commit_hash ? pipeline.commit_hash.substring(0, 8) : 'Manual Trigger'}
                                                </span>
                                            </span>
                                        </div>
                                        <div className="flex items-center gap-4 mt-1 text-xs text-gray-500">
                                            <span className="flex items-center gap-1">
                                                <GitBranch size={12} />
                                                {pipeline.branch}
                                            </span>
                                            <span className="flex items-center gap-1">
                                                <Clock size={12} />
                                                {formatDistanceToNow(new Date(pipeline.created_at), { addSuffix: true })}
                                            </span>
                                        </div>
                                    </div>
                                </div>
                            </div>
                        </Link>
                    ))}
                </div>
            )}
        </div>
      </div>
    </div>
  );
}