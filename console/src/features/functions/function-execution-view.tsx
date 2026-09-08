"use client";

import Link from "next/link";
import { useCallback } from "react";
import { api, unwrap } from "@/api/client";
import { useFunctionExecution } from "@/api/queries";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { LogViewer, type LogLine } from "@/components/log-viewer";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { HttpStatusBadge, StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { formatDate, formatDuration } from "@/lib/format";
import { BackLink } from "@/features/resources/detail-shared";

function formatJson(value: unknown) {
  if (value === undefined) return "—";
  try {
    return JSON.stringify(value, null, 2) ?? String(value);
  } catch {
    return String(value);
  }
}

function executionDuration(
  startedAt: string | null | undefined,
  finishedAt: string | null | undefined,
) {
  if (!startedAt || !finishedAt) return null;
  const duration =
    new Date(finishedAt).valueOf() - new Date(startedAt).valueOf();
  return Number.isFinite(duration) && duration >= 0 ? duration : null;
}

export function FunctionExecutionView({
  organizationId,
  projectId,
  functionId,
  executionId,
}: {
  organizationId: string;
  projectId: string;
  functionId: string;
  executionId: string;
}) {
  const query = useFunctionExecution(projectId, functionId, executionId);
  const fetchLogs = useCallback(
    async (after?: number): Promise<LogLine[]> => {
      const result = await api.GET(
        "/v1/projects/{projectID}/functions/{functionID}/executions/{executionID}/logs",
        {
          params: {
            path: {
              projectID: projectId,
              functionID: functionId,
              executionID: executionId,
            },
            query: after === undefined ? {} : { after },
          },
        },
      );
      const data = await unwrap(result);
      return (data?.logs ?? []) as LogLine[];
    },
    [executionId, functionId, projectId],
  );
  const execution = query.data?.execution;
  const base = `/organizations/${organizationId}/projects/${projectId}`;

  if (query.error)
    return <ErrorState error={query.error} retry={() => query.refetch()} />;
  if (query.isPending) return <LoadingState />;
  if (!execution)
    return (
      <EmptyState
        title="Execution not found"
        description="The execution may have been removed or is outside this function."
      />
    );

  return (
    <>
      <BackLink
        href={`${base}/functions/${functionId}`}
        label="Back to function"
      />
      <PageHeader
        eyebrow="Function execution"
        title="Execution"
        description="Inspect the runtime result and incremental execution logs."
        actions={<StatusBadge status={execution.status} />}
      />
      <div className="mb-5 flex flex-wrap items-center gap-2">
        <ResourceId id={execution.id} label="Execution ID" />
        <span className="text-xs text-slate-600">
          Created {formatDate(execution.created_at)}
        </span>
      </div>
      <div className="grid gap-4 md:grid-cols-4">
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-slate-500">Trigger</p>
            <p className="mt-2 font-mono text-sm text-white">
              {execution.trigger}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-slate-500">Response</p>
            <div className="mt-2">
              {execution.response_status !== null &&
              execution.response_status !== undefined ? (
                <HttpStatusBadge status={execution.response_status} />
              ) : (
                <span className="text-sm text-slate-500">—</span>
              )}
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-slate-500">Duration</p>
            <p className="mt-2 text-sm text-white">
              {formatDuration(
                executionDuration(execution.started_at, execution.finished_at),
              )}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-slate-500">Finished</p>
            <p className="mt-2 text-sm text-white">
              {formatDate(execution.finished_at)}
            </p>
          </CardContent>
        </Card>
      </div>
      {execution.error_message || execution.status === "failed" ? (
        <Card className="mt-5 border-rose-300/20 bg-rose-400/[0.04]">
          <CardContent className="p-5">
            <p className="text-xs uppercase tracking-[0.14em] text-rose-300">
              Execution failed
            </p>
            <p className="mt-2 whitespace-pre-wrap text-sm text-rose-100">
              {execution.error_message ??
                "The execution did not complete successfully."}
            </p>
          </CardContent>
        </Card>
      ) : null}
      <div className="mt-5 grid gap-5 xl:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Input</CardTitle>
          </CardHeader>
          <CardContent>
            <pre className="max-h-80 overflow-auto rounded-lg border border-stealth-border bg-stealth-bg p-4 font-mono text-xs leading-6 text-slate-300">
              {formatJson(execution.input_json)}
            </pre>
          </CardContent>
        </Card>
        {execution.output_json !== undefined ? (
          <Card>
            <CardHeader>
              <CardTitle>Output</CardTitle>
            </CardHeader>
            <CardContent>
              <pre className="max-h-80 overflow-auto rounded-lg border border-stealth-border bg-stealth-bg p-4 font-mono text-xs leading-6 text-slate-300">
                {formatJson(execution.output_json)}
              </pre>
            </CardContent>
          </Card>
        ) : null}
      </div>
      <div id="execution-logs" className="mt-5 scroll-mt-6">
        <LogViewer
          key={executionId}
          title="Execution logs"
          description="Runtime output for this function execution."
          fetchPage={fetchLogs}
          emptyMessage="No execution logs yet. Logs will appear when the runtime starts."
        />
      </div>
      <Button asChild variant="ghost" className="mt-3">
        <Link href={`${base}/functions/${functionId}`}>Open function</Link>
      </Button>
    </>
  );
}
