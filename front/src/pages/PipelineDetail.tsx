import React, { useState, useEffect, useRef } from 'react';
import { useParams, Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { 
  getPipeline, 
  getJobs, 
  getJobLogs, 
  Job, 
  getDeployment, 
  getDeploymentLogs 
} from '../lib/api';
import { 
  CheckCircle2, 
  XCircle, 
  Loader2, 
  Clock, 
  GitCommit, 
  GitBranch,
  Terminal,
  ChevronRight,
  X,
  Maximize2,
  Server,
  ArrowRight
} from 'lucide-react';
import { formatDistanceToNow, format } from 'date-fns';
import { clsx } from 'clsx';

const StatusIcon = ({ status, size = 16 }: { status: string; size?: number }) => {
  switch (status) {
    case 'success':
      return <CheckCircle2 size={size} className="text-green-500" />;
    case 'failed':
      return <XCircle size={size} className="text-red-500" />;
    case 'running':
    case 'deploying':
      return <Loader2 size={size} className="text-blue-500 animate-spin" />;
    default:
      return <Clock size={size} className="text-gray-400" />;
  }
};

const JobCard = ({ job, onClick, isActive }: { job: Job; onClick: () => void; isActive: boolean }) => {
  return (
    <button
      onClick={onClick}
      className={clsx(
        "w-full text-left p-3 rounded-lg border transition-all relative group",
        isActive 
          ? "border-blue-500 ring-1 ring-blue-500 bg-blue-50/50" 
          : "border-gray-200 hover:border-blue-300 hover:shadow-sm bg-white"
      )}
    >
      <div className="flex items-center justify-between mb-2">
        <span className="font-medium text-sm text-gray-900 truncate pr-2" title={job.name}>
          {job.name}
        </span>
        <StatusIcon status={job.status} />
      </div>
      
      <div className="flex items-center gap-2 text-xs text-gray-500">
        <Clock size={12} />
        {job.started_at 
          ? formatDistanceToNow(new Date(job.started_at), { addSuffix: true })
          : 'Pending'
        }
      </div>
      
      {/* Connector line for graph visualization */}
      <div className="absolute top-1/2 -right-6 w-6 h-0.5 bg-gray-200 hidden group-last:hidden md:block" />
    </button>
  );
};

export function PipelineDetail() {
  const { projectId, pipelineId } = useParams<{ projectId: string; pipelineId: string }>();
  const pId = parseInt(projectId || '0');
  const pipeId = parseInt(pipelineId || '0');
  
  const [selectedJob, setSelectedJob] = useState<Job | null>(null);
  const [showDeploymentLogs, setShowDeploymentLogs] = useState(false);
  const logsEndRef = useRef<HTMLDivElement>(null);

  // Queries
  const { data: pipeline, isLoading: isPipelineLoading } = useQuery({
    queryKey: ['pipeline', pId, pipeId],
    queryFn: () => getPipeline(pId, pipeId),
    refetchInterval: 2000,
  });

  const { data: jobs, isLoading: isJobsLoading } = useQuery({
    queryKey: ['jobs', pId, pipeId],
    queryFn: () => getJobs(pId, pipeId),
    refetchInterval: 2000,
  });

  const { data: deployment } = useQuery({
    queryKey: ['deployment', pId, pipeId],
    queryFn: () => getDeployment(pId, pipeId),
    refetchInterval: 2000,
  });

  // Logs Query (Job)
  const { data: jobLogs } = useQuery({
    queryKey: ['jobLogs', pId, pipeId, selectedJob?.id],
    queryFn: () => getJobLogs(pId, pipeId, selectedJob!.id),
    enabled: !!selectedJob,
    refetchInterval: (query) => {
        // Stop polling if job is finished
        if (selectedJob?.status === 'success' || selectedJob?.status === 'failed') return false;
        return 1000;
    }
  });

  // Logs Query (Deployment)
  const { data: deployLogs } = useQuery({
    queryKey: ['deployLogs', pId, pipeId],
    queryFn: () => getDeploymentLogs(pId, pipeId),
    enabled: showDeploymentLogs,
    refetchInterval: (query) => {
        if (deployment?.status === 'success' || deployment?.status === 'failed') return false;
        return 1000;
    }
  });

  // Auto-scroll logs
  useEffect(() => {
    if (logsEndRef.current) {
      logsEndRef.current.scrollIntoView({ behavior: 'smooth' });
    }
  }, [jobLogs, deployLogs]);

  // Group jobs by stage
  const stages = React.useMemo(() => {
    if (!jobs) return {};
    const order = ['build', 'test', 'scan', 'deploy']; // Standard order, others appended
    const grouped = jobs.reduce((acc, job) => {
      if (!acc[job.stage]) acc[job.stage] = [];
      acc[job.stage].push(job);
      return acc;
    }, {} as Record<string, Job[]>);

    // Sort stages based on predefined order + custom ones
    const sortedStages: Record<string, Job[]> = {};
    const uniqueStages = Array.from(new Set([...order, ...Object.keys(grouped)]));
    
    uniqueStages.forEach(stage => {
      if (grouped[stage]) {
        sortedStages[stage] = grouped[stage];
      }
    });

    return sortedStages;
  }, [jobs]);

  const activeLogs = showDeploymentLogs 
    ? deployLogs?.map(l => l.content) 
    : jobLogs?.map(l => l.content);

  const activeTitle = showDeploymentLogs
    ? `Deployment Logs`
    : selectedJob 
        ? `${selectedJob.name} Logs` 
        : null;

  if (isPipelineLoading || isJobsLoading) {
    return (
        <div className="flex items-center justify-center h-64">
            <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-600"></div>
        </div>
    );
  }

  if (!pipeline) return <div>Pipeline not found</div>;

  return (
    <div className="flex flex-col h-[calc(100vh-4rem)]">
      {/* Header */}
      <div className="bg-white border-b border-gray-200 px-6 py-4 flex-none">
        <div className="flex items-center gap-2 text-sm text-gray-500 mb-2">
            <Link to={`/projects/${pId}`} className="hover:text-blue-600 transition-colors">Project #{pId}</Link>
            <ChevronRight size={14} />
            <span>Pipeline #{pipeId}</span>
        </div>
        
        <div className="flex items-center justify-between">
            <div className="flex items-center gap-4">
                <div className={clsx(
                    "flex items-center gap-2 px-3 py-1.5 rounded-full border text-sm font-medium",
                    pipeline.status === 'success' ? "bg-green-50 border-green-200 text-green-700" :
                    pipeline.status === 'failed' ? "bg-red-50 border-red-200 text-red-700" :
                    pipeline.status === 'running' ? "bg-blue-50 border-blue-200 text-blue-700" :
                    "bg-gray-50 border-gray-200 text-gray-700"
                )}>
                    <StatusIcon status={pipeline.status} />
                    <span className="capitalize">{pipeline.status}</span>
                </div>

                <div className="flex items-center gap-6 text-sm text-gray-600">
                    <div className="flex items-center gap-2">
                        <GitCommit size={16} className="text-gray-400" />
                        <span className="font-mono">{pipeline.commit_hash?.substring(0, 8) || 'Manual'}</span>
                    </div>
                    <div className="flex items-center gap-2">
                        <GitBranch size={16} className="text-gray-400" />
                        <span>{pipeline.branch}</span>
                    </div>
                    <div className="flex items-center gap-2">
                        <Clock size={16} className="text-gray-400" />
                        <span>{formatDistanceToNow(new Date(pipeline.created_at), { addSuffix: true })}</span>
                    </div>
                </div>
            </div>
            
            {deployment && (
                <button
                    onClick={() => {
                        setSelectedJob(null);
                        setShowDeploymentLogs(true);
                    }}
                    className={clsx(
                        "flex items-center gap-2 px-3 py-1.5 rounded-lg border text-sm font-medium transition-colors",
                        showDeploymentLogs
                            ? "bg-purple-50 border-purple-200 text-purple-700"
                            : "bg-white border-gray-200 text-gray-700 hover:bg-gray-50"
                    )}
                >
                    <Server size={16} />
                    Deployment: <span className="capitalize">{deployment.status}</span>
                </button>
            )}
        </div>
      </div>

      {/* Main Content Area */}
      <div className="flex-1 flex overflow-hidden">
        
        {/* Pipeline Graph (Left/Top) */}
        <div className="flex-1 overflow-auto bg-gray-50 p-8">
            <div className="flex gap-12 min-w-max pb-8">
                {Object.entries(stages).map(([stageName, stageJobs], index, arr) => (
                    <div key={stageName} className="flex flex-col gap-4 w-64 relative">
                        {/* Stage Header */}
                        <div className="flex items-center justify-center mb-2">
                            <span className="px-3 py-1 rounded-full bg-gray-200 text-gray-600 text-xs font-bold uppercase tracking-wide">
                                {stageName}
                            </span>
                        </div>

                        {/* Connection Line to Next Stage */}
                        {index < arr.length - 1 && (
                            <div className="hidden md:block absolute top-12 left-full w-12 h-0.5 bg-gray-300 -translate-y-1/2 z-0" />
                        )}

                        {/* Jobs List */}
                        <div className="flex flex-col gap-3 relative z-10">
                            {stageJobs.map((job) => (
                                <JobCard 
                                    key={job.id} 
                                    job={job} 
                                    isActive={selectedJob?.id === job.id}
                                    onClick={() => {
                                        setShowDeploymentLogs(false);
                                        setSelectedJob(job);
                                    }}
                                />
                            ))}
                        </div>
                    </div>
                ))}
                
                {/* Deployment Node (Visual) */}
                {deployment && (
                    <div className="flex flex-col gap-4 w-64 relative">
                         <div className="flex items-center justify-center mb-2">
                            <span className="px-3 py-1 rounded-full bg-purple-100 text-purple-700 text-xs font-bold uppercase tracking-wide">
                                Production
                            </span>
                        </div>
                        <button
                            onClick={() => {
                                setSelectedJob(null);
                                setShowDeploymentLogs(true);
                            }}
                            className={clsx(
                                "w-full text-left p-3 rounded-lg border transition-all relative",
                                showDeploymentLogs 
                                  ? "border-purple-500 ring-1 ring-purple-500 bg-purple-50/50" 
                                  : "border-gray-200 hover:border-purple-300 bg-white"
                            )}
                        >
                             <div className="flex items-center justify-between mb-2">
                                <span className="font-medium text-sm text-gray-900">Deploy</span>
                                <StatusIcon status={deployment.status} />
                            </div>
                             <div className="flex items-center gap-2 text-xs text-gray-500">
                                <Server size={12} />
                                {deployment.started_at 
                                ? formatDistanceToNow(new Date(deployment.started_at), { addSuffix: true })
                                : 'Pending'
                                }
                            </div>
                        </button>
                    </div>
                )}
            </div>
        </div>

        {/* Logs Panel (Right/Bottom) */}
        {(selectedJob || showDeploymentLogs) && (
            <div className="w-[500px] border-l border-gray-200 bg-[#1e1e1e] flex flex-col shadow-xl flex-none transition-all duration-300">
                <div className="flex items-center justify-between px-4 py-3 bg-[#252526] border-b border-[#3e3e3e]">
                    <div className="flex items-center gap-2 text-gray-200">
                        <Terminal size={16} />
                        <span className="font-mono text-sm">{activeTitle}</span>
                    </div>
                    <button 
                        onClick={() => {
                            setSelectedJob(null);
                            setShowDeploymentLogs(false);
                        }}
                        className="text-gray-400 hover:text-white transition-colors"
                    >
                        <X size={16} />
                    </button>
                </div>
                
                <div className="flex-1 overflow-auto p-4 font-mono text-xs leading-relaxed">
                    {activeLogs && activeLogs.length > 0 ? (
                        activeLogs.map((log, i) => (
                            <div key={i} className="text-gray-300 whitespace-pre-wrap break-all border-l-2 border-transparent pl-2 hover:bg-[#2a2d2e]">
                                <span className="text-[#569cd6] mr-3 select-none opacity-50">{i + 1}</span>
                                {log}
                            </div>
                        ))
                    ) : (
                        <div className="text-gray-500 italic">No logs available yet...</div>
                    )}
                    <div ref={logsEndRef} />
                </div>
            </div>
        )}
      </div>
    </div>
  );
}