"use client";
import Link from "next/link";
import type { ColumnDef } from "@tanstack/react-table";
import { Play } from "lucide-react";
import { useCallback, useState } from "react";
import { toast } from "sonner";
import { api, unwrap } from "@/api/client";
import { nextCursor } from "@/api/pagination";
import { useCancelAgentRun, useCreateAgentRun } from "@/api/mutations";
import { useAgentRun, useAgentRuns } from "@/api/queries";
import type { AgentRun } from "@/api/types";
import { DataTable } from "@/components/data-table";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { LogViewer, type LogLine } from "@/components/log-viewer";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
import { Textarea } from "@/components/ui/textarea";
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
  const runsNavigation = useCursorPagination("runs_cursor");
  const runs = useAgentRuns(agentId, { cursor: runsNavigation.cursor });
  const create = useCreateAgentRun(agentId);
  const [prompt, setPrompt] = useState("");
  const columns: ColumnDef<AgentRun, unknown>[] = [
    {
      accessorKey: "id",
      header: "Run",
      cell: ({ row }) => (
        <Link
          href={`/organizations/${organizationId}/projects/${projectId}/agents/${agentId}/runs/${row.original.id}`}
          className="font-mono text-xs text-cyan-300 hover:text-cyan-200"
        >
          {row.original.id.slice(0, 12)}…
        </Link>
      ),
    },
    {
      accessorKey: "status",
      header: "Status",
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: "prompt",
      header: "Prompt",
      cell: ({ row }) => (
        <span className="block max-w-lg truncate text-slate-300">
          {row.original.prompt}
        </span>
      ),
    },
    {
      accessorKey: "created_at",
      header: "Created",
      cell: ({ row }) => formatDate(row.original.created_at),
    },
  ];
  if (runs.error)
    return <ErrorState error={runs.error} retry={() => runs.refetch()} />;
  return (
    <>
      <BackLink
        href={`/organizations/${organizationId}/projects/${projectId}/agents`}
        label="Back to agents"
      />
      <PageHeader
        eyebrow="Agent runs"
        title="Task runner"
        description="Runs are durable queued tasks, not chat sessions. The catalog determines whether provider execution is ready."
      />
      <Card className="mb-5">
        <CardHeader>
          <CardTitle>Queue a run</CardTitle>
        </CardHeader>
        <CardContent>
          <Textarea
            className="min-h-24"
            value={prompt}
            onChange={(event) => setPrompt(event.target.value)}
            placeholder="Describe the task for this agent…"
            aria-label="Agent task prompt"
          />
          <Button
            className="mt-3"
            disabled={!prompt.trim() || create.isPending}
            onClick={() =>
              create.mutate(
                { prompt },
                {
                  onSuccess: () => {
                    setPrompt("");
                    toast.success("Run queued");
                  },
                },
              )
            }
          >
            <Play className="size-4" /> Queue run
          </Button>
        </CardContent>
      </Card>
      <Card>
        <DataTable
          data={runs.data?.runs ?? []}
          columns={columns}
          loading={runs.isLoading}
          empty="No runs yet. Create a run to queue a task for this agent."
          serverPagination={pageControls(
            runsNavigation,
            nextCursor(runs.data),
            runs.isFetching,
          )}
        />
      </Card>
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
      return (data?.logs ?? []) as LogLine[];
    },
    [agentId, runId],
  );
  if (query.error)
    return <ErrorState error={query.error} retry={() => query.refetch()} />;
  if (!run)
    return (
      <EmptyState
        title="Run not found"
        description="The run may have been removed or is outside this agent."
      />
    );
  const active = run.status === "queued" || run.status === "running";
  return (
    <>
      <BackLink
        href={`/organizations/${organizationId}/projects/${projectId}/agents/${agentId}/runs`}
        label="Back to runs"
      />
      <PageHeader
        eyebrow="Agent run"
        title="Task execution"
        description="A durable coding-agent task with backend-owned progress, logs, and output."
        actions={
          <div className="flex items-center gap-2">
            <StatusBadge status={run.status} />
            {active ? (
              <Button
                variant="outline"
                disabled={cancel.isPending}
                onClick={() =>
                  cancel.mutate(undefined, {
                    onSuccess: () => toast.success("Run cancelled"),
                    onError: (error) =>
                      toast.error(
                        error instanceof Error
                          ? error.message
                          : "Unable to cancel run",
                      ),
                  })
                }
              >
                Cancel run
              </Button>
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
      <div className="grid gap-4 md:grid-cols-4">
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
            <p className="text-xs text-slate-500">Steps</p>
            <p className="mt-2 text-sm text-white">{run.steps.length}</p>
          </CardContent>
        </Card>
      </div>
      <div className="mt-5 grid gap-5 xl:grid-cols-[1fr_1.1fr]">
        <Card>
          <CardHeader>
            <CardTitle>Prompt</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="whitespace-pre-wrap text-sm leading-6 text-slate-300">
              {run.prompt}
            </p>
            {run.error_message ? (
              <p className="mt-4 rounded-lg border border-rose-300/20 bg-rose-400/10 p-3 text-sm text-rose-200">
                {run.error_message}
              </p>
            ) : null}
            {run.output_text ? (
              <div className="mt-4">
                <p className="text-xs uppercase tracking-[0.14em] text-slate-600">
                  Result
                </p>
                <p className="mt-2 whitespace-pre-wrap text-sm leading-6 text-slate-300">
                  {run.output_text}
                </p>
              </div>
            ) : null}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Progress</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2">
            {run.steps.length ? (
              run.steps.map((step) => (
                <div
                  key={step.id}
                  className="flex items-start gap-3 rounded-lg border border-stealth-border p-3"
                >
                  <span
                    className={
                      step.status === "done"
                        ? "mt-1 size-2 rounded-full bg-emerald-300"
                        : "mt-1 size-2 rounded-full bg-amber-300"
                    }
                  />
                  <div>
                    <p className="text-sm text-white">{step.label}</p>
                    <p className="mt-1 text-xs text-slate-500">
                      {step.type} · {step.target}
                    </p>
                  </div>
                </div>
              ))
            ) : (
              <p className="text-sm text-slate-500">
                The worker has not reported steps yet.
              </p>
            )}
          </CardContent>
        </Card>
      </div>
      <div className="mt-5">
        <LogViewer
          title="Run logs"
          description="Incremental worker log stream"
          fetchPage={logFetcher}
          enabled
        />
      </div>
    </>
  );
}
