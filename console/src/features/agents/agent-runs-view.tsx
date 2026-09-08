"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import type { ColumnDef } from "@tanstack/react-table";
import {
  CheckCircle2,
  CircleDashed,
  Play,
  SquareArrowOutUpRight,
} from "lucide-react";
import { useCallback, useState } from "react";
import { toast } from "sonner";
import { api, unwrap } from "@/api/client";
import { nextCursor } from "@/api/pagination";
import { useCancelAgentRun, useCreateAgentRun } from "@/api/mutations";
import { useAgentRun, useAgentRuns } from "@/api/queries";
import type { AgentRun } from "@/api/types";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DataTable } from "@/components/data-table";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { EmptyState } from "@/components/empty-state";
import { ErrorState, errorMessage } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { LogViewer, type LogLine } from "@/components/log-viewer";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Textarea } from "@/components/ui/textarea";
import {
  agentRunDurationMs,
  formatAgentRunDuration,
  isAgentRunActive,
} from "@/features/agents/agent-run-state";
import { AgentRunStatusBadge } from "@/features/agents/agent-run-status-badge";
import { formatDate, formatDuration } from "@/lib/format";
import { pageControls } from "@/lib/pagination";
import { BackLink } from "@/features/resources/detail-shared";

export function AgentRunsView({
  organizationId,
  projectId,
  agentId,
}: {
  organizationId: string;
  projectId: string;
  agentId: string;
}) {
  const router = useRouter();
  const runsNavigation = useCursorPagination("runs_cursor");
  const runs = useAgentRuns(agentId, { cursor: runsNavigation.cursor });
  const create = useCreateAgentRun(agentId);
  const [prompt, setPrompt] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const runBase = `${base}/agents/${agentId}/runs`;

  const handleCreateRun = async () => {
    const task = prompt.trim();
    if (!task) return;

    setCreateError(null);
    try {
      const result = await create.mutateAsync({ prompt: task });
      if (!result?.run.id) {
        throw new Error("The API did not return the queued run.");
      }
      setPrompt("");
      toast.success("Run queued");
      router.push(`${runBase}/${result.run.id}`);
    } catch (error) {
      const message = errorMessage(error);
      setCreateError(message);
      toast.error(`Could not start run. ${message}`);
    }
  };

  const columns: ColumnDef<AgentRun, unknown>[] = [
    {
      accessorKey: "status",
      header: "Status",
      cell: ({ row }) => <AgentRunStatusBadge status={row.original.status} />,
    },
    {
      accessorKey: "prompt",
      header: "Task",
      cell: ({ row }) => (
        <Link
          href={`${runBase}/${row.original.id}`}
          className="block max-w-lg truncate text-slate-200 hover:text-cyan-200"
          title={row.original.prompt}
        >
          {row.original.prompt}
        </Link>
      ),
    },
    {
      accessorKey: "created_at",
      header: "Created",
      cell: ({ row }) => formatDate(row.original.created_at),
    },
    {
      accessorKey: "started_at",
      header: "Started",
      cell: ({ row }) => formatDate(row.original.started_at),
    },
    {
      accessorKey: "finished_at",
      header: "Finished",
      cell: ({ row }) => formatDate(row.original.finished_at),
    },
    {
      id: "duration",
      header: "Duration",
      cell: ({ row }) => formatAgentRunDuration(row.original),
    },
    {
      id: "run_id",
      header: "Run ID",
      cell: ({ row }) => <ResourceId id={row.original.id} label="Run ID" />,
    },
  ];

  const hasRows =
    (runs.data?.runs.length ?? 0) > 0 ||
    runsNavigation.canFirst ||
    Boolean(nextCursor(runs.data));

  if (runs.error)
    return (
      <ErrorState
        title="Could not load agent runs"
        error={runs.error}
        retry={() => runs.refetch()}
      />
    );

  return (
    <>
      <BackLink href={`${base}/agents/${agentId}`} label="Back to agent" />
      <PageHeader
        eyebrow="Agent runs"
        title="Run agent"
        description="Create durable task executions and inspect their backend-owned status, steps, logs, and output."
      />
      <Card className="mb-5">
        <CardHeader>
          <CardTitle>Task</CardTitle>
        </CardHeader>
        <CardContent>
          <Textarea
            id="agent-task-prompt"
            className="min-h-24"
            value={prompt}
            onChange={(event) => {
              setPrompt(event.target.value);
              if (createError) setCreateError(null);
            }}
            placeholder="Describe the developer task for this agent…"
            aria-label="Agent task prompt"
          />
          {createError ? (
            <p
              className="mt-3 rounded-lg border border-rose-300/20 bg-rose-400/10 p-3 text-sm text-rose-200"
              role="alert"
            >
              Could not start run. {createError}
            </p>
          ) : null}
          <Button
            className="mt-3"
            disabled={!prompt.trim() || create.isPending}
            onClick={() => void handleCreateRun()}
          >
            <Play className="size-4" /> Run agent
          </Button>
        </CardContent>
      </Card>
      {hasRows ? (
        <Card>
          <DataTable
            data={runs.data?.runs ?? []}
            columns={columns}
            loading={runs.isLoading}
            serverPagination={pageControls(
              runsNavigation,
              nextCursor(runs.data),
              runs.isFetching,
            )}
          />
        </Card>
      ) : runs.isLoading ? (
        <Card>
          <DataTable data={[]} columns={columns} loading />
        </Card>
      ) : (
        <EmptyState
          title="No runs yet"
          description="Run this agent to start its first task."
          action={() => document.getElementById("agent-task-prompt")?.focus()}
          actionLabel="Run agent"
        />
      )}
    </>
  );
}

export function AgentRunDetailView({
  organizationId,
  projectId,
  agentId,
  runId,
}: {
  organizationId: string;
  projectId: string;
  agentId: string;
  runId: string;
}) {
  const query = useAgentRun(agentId, runId);
  const cancel = useCancelAgentRun(agentId, runId);
  const run = query.data?.run;
  const logFetcher = useCallback(
    async (after?: number): Promise<LogLine[]> => {
      const result = await api.GET("/v1/agents/{agentID}/runs/{runID}/logs", {
        params: {
          path: { agentID: agentId, runID: runId },
          query: after === undefined ? { limit: 100 } : { limit: 100, after },
        },
      });
      const data = await unwrap(result);
      return (data?.logs ?? []).map((log) => ({
        sequence: log.sequence,
        level: log.level,
        message: log.message,
        created_at: log.created_at,
      }));
    },
    [agentId, runId],
  );

  if (query.error)
    return (
      <ErrorState
        title="Could not load agent run"
        error={query.error}
        retry={() => query.refetch()}
      />
    );
  if (query.isPending) return <LoadingState rows={5} />;
  if (!run)
    return (
      <EmptyState
        title="Run not found"
        description="The run may have been removed or is outside this agent."
      />
    );

  const active = isAgentRunActive(run.status);
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const runBase = `${base}/agents/${agentId}/runs`;
  const duration = agentRunDurationMs(run);

  const handleCancel = async () => {
    try {
      await cancel.mutateAsync();
      toast.success("Run cancelled");
    } catch (error) {
      toast.error(`Could not cancel run. ${errorMessage(error)}`);
    }
  };

  const statusMessage = {
    queued: "This run is waiting for a worker.",
    running: "The worker is processing this task.",
    completed: "The task completed successfully.",
    failed: "The task did not complete successfully.",
    cancelled: "This run was cancelled before completion.",
  }[run.status];

  return (
    <>
      <BackLink href={runBase} label="Back to runs" />
      <PageHeader
        eyebrow="Agent run"
        title={`Run ${run.id.slice(0, 12)}…`}
        description="A durable developer task with backend-owned progress, logs, and output."
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <AgentRunStatusBadge status={run.status} />
            {active ? (
              <ConfirmDialog
                trigger={
                  <Button variant="outline" disabled={cancel.isPending}>
                    Cancel run
                  </Button>
                }
                title="Cancel this run?"
                description="The worker will stop processing this queued or running task."
                confirmLabel="Cancel run"
                pending={cancel.isPending}
                onConfirm={handleCancel}
              />
            ) : null}
          </div>
        }
      />
      <div className="mb-5 flex flex-wrap items-center gap-2">
        <ResourceId id={run.id} label="Run ID" />
        <span className="text-xs text-slate-600">
          Updated {formatDate(run.updated_at)}
        </span>
      </div>

      <Card className="mb-5 border-cyan-300/15 bg-cyan-300/[0.03]">
        <CardContent className="flex items-start gap-3 p-5">
          {active ? (
            <CircleDashed className="mt-0.5 size-5 text-cyan-300" />
          ) : run.status === "completed" ? (
            <CheckCircle2 className="mt-0.5 size-5 text-emerald-300" />
          ) : null}
          <div>
            <p className="text-sm font-medium text-white">
              {run.status === "failed" ? "Run failed" : statusMessage}
            </p>
            <p className="mt-1 text-xs leading-5 text-slate-400">
              {run.status === "queued"
                ? "No worker has started this task yet."
                : run.status === "running"
                  ? `Started ${formatDate(run.started_at)}`
                  : run.status === "completed"
                    ? `Duration ${formatDuration(duration)}`
                    : statusMessage}
            </p>
          </div>
        </CardContent>
      </Card>

      <div className="grid gap-4 md:grid-cols-5">
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-slate-500">Created</p>
            <p className="mt-2 text-sm text-white">
              {formatDate(run.created_at)}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-slate-500">Queued</p>
            <p className="mt-2 text-sm text-white">
              {formatDate(run.queued_at)}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-slate-500">Started</p>
            <p className="mt-2 text-sm text-white">
              {formatDate(run.started_at)}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-slate-500">Finished</p>
            <p className="mt-2 text-sm text-white">
              {formatDate(run.finished_at)}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-slate-500">Duration</p>
            <p className="mt-2 text-sm text-white">
              {formatAgentRunDuration(run)}
            </p>
          </CardContent>
        </Card>
      </div>

      <div className="mt-5 grid gap-5 xl:grid-cols-[1fr_1.1fr]">
        <Card>
          <CardHeader>
            <CardTitle>Task</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="whitespace-pre-wrap text-sm leading-6 text-slate-300">
              {run.prompt}
            </p>
            {run.status === "failed" ? (
              <div className="mt-5 rounded-lg border border-rose-300/20 bg-rose-400/10 p-3">
                <p className="text-sm font-medium text-rose-200">Run failed</p>
                <p className="mt-1 whitespace-pre-wrap text-sm leading-6 text-rose-200/80">
                  {run.error_message ??
                    "The task did not complete successfully."}
                </p>
              </div>
            ) : null}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Steps</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2">
            {run.steps.length ? (
              run.steps.map((step) => (
                <div
                  key={step.id}
                  className="flex items-start gap-3 rounded-lg border border-stealth-border p-3"
                >
                  {step.status === "done" ? (
                    <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-emerald-300" />
                  ) : (
                    <CircleDashed className="mt-0.5 size-4 shrink-0 text-amber-300" />
                  )}
                  <div className="min-w-0">
                    <p className="text-sm text-white">{step.label}</p>
                    <p className="mt-1 break-words text-xs text-slate-500">
                      {step.status === "done" ? "Done" : "Pending"} ·{" "}
                      {step.type} · {step.target}
                    </p>
                  </div>
                </div>
              ))
            ) : (
              <p className="text-sm text-slate-500">
                No run steps have been reported by the worker.
              </p>
            )}
          </CardContent>
        </Card>
      </div>

      {run.output_text !== null && run.output_text !== undefined ? (
        <Card className="mt-5">
          <CardHeader>
            <CardTitle>Result</CardTitle>
          </CardHeader>
          <CardContent>
            <pre className="max-h-[32rem] overflow-auto whitespace-pre-wrap break-words rounded-lg border border-stealth-border bg-stealth-bg p-4 font-mono text-xs leading-6 text-slate-300">
              {run.output_text || "No output was returned."}
            </pre>
          </CardContent>
        </Card>
      ) : null}

      {run.changes.length ? (
        <Card className="mt-5">
          <CardHeader>
            <CardTitle>Changes</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2">
            {run.changes.map((change) => (
              <div
                key={`${change.status}:${change.path}`}
                className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-stealth-border p-3"
              >
                <span className="break-all font-mono text-xs text-slate-300">
                  {change.path}
                </span>
                <span className="text-xs text-slate-500">
                  {change.status} · +{change.additions} / -{change.deletions}
                </span>
              </div>
            ))}
          </CardContent>
        </Card>
      ) : null}

      <div className="mt-5">
        <LogViewer
          title="Run logs"
          description="Incremental worker log stream"
          fetchPage={logFetcher}
          enabled
          polling={active}
          emptyMessage="No run logs yet. Logs will appear after the agent starts working."
        />
      </div>

      <div className="mt-5 flex justify-end">
        <Button asChild variant="ghost" size="sm">
          <Link href={runBase}>
            <SquareArrowOutUpRight className="size-3.5" /> Open run history
          </Link>
        </Button>
      </div>
    </>
  );
}
