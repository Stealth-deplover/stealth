"use client";

import { useMemo } from "react";
import { useAdminOperations } from "@/api/queries";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { StatusBadge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { formatDate, formatDuration } from "@/lib/format";
import { AdminShell } from "./admin-shell";
import { AdminTimeRange, useAdminTimeRange } from "./admin-time-range";

export function AdminOperationsView() {
  const timeRange = useAdminTimeRange();
  const query = useMemo(
    () => ({ ...timeRange.query, limit: 100 }),
    [timeRange.query],
  );
  const operations = useAdminOperations(query, {
    refetchInterval: timeRange.refreshInterval,
  });

  return (
    <AdminShell>
      <PageHeader
        eyebrow="Admin / Operations"
        title="Operations"
        description="Durable deployments, executions, Agent runs, cleanup jobs, and backups from the control plane."
        actions={
          <AdminTimeRange
            rangeKey={timeRange.rangeKey}
            refreshKey={timeRange.refreshKey}
            onRangeChange={timeRange.setRange}
            onRefreshChange={timeRange.setRefresh}
          />
        }
      />
      {operations.isPending ? <LoadingState rows={6} /> : null}
      {operations.error ? (
        <ErrorState
          title="Could not load operations"
          error={operations.error}
          retry={() => operations.refetch()}
        />
      ) : null}
      {operations.data && !operations.data.items.length ? (
        <Card>
          <CardContent className="p-8 text-center text-sm text-fog">
            No durable operations were recorded in this time window.
          </CardContent>
        </Card>
      ) : null}
      {operations.data?.items.length ? (
        <Card className="overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[920px] text-left text-sm">
              <thead className="border-b border-graphite bg-white/[0.02] text-xs uppercase tracking-[0.1em] text-fog">
                <tr>
                  <th className="px-4 py-3 font-medium">Started</th>
                  <th className="px-4 py-3 font-medium">Operation</th>
                  <th className="px-4 py-3 font-medium">Project</th>
                  <th className="px-4 py-3 font-medium">Status</th>
                  <th className="px-4 py-3 font-medium">Duration</th>
                  <th className="px-4 py-3 font-medium">Error</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-graphite">
                {operations.data.items.map((operation) => (
                  <tr
                    key={`${operation.kind}-${operation.id}`}
                    className="align-top hover:bg-white/[0.025]"
                  >
                    <td className="whitespace-nowrap px-4 py-3 font-mono text-xs text-fog">
                      {formatDate(operation.created_at)}
                    </td>
                    <td className="px-4 py-3">
                      <p className="text-mist">{operation.name}</p>
                      <p className="mt-1 font-mono text-[11px] text-fog">
                        {operation.kind} · {operation.id}
                      </p>
                    </td>
                    <td className="px-4 py-3 text-mist">
                      {operation.project_name ?? "Instance"}
                    </td>
                    <td className="px-4 py-3">
                      <StatusBadge status={operation.status} />
                    </td>
                    <td className="whitespace-nowrap px-4 py-3 font-mono text-xs tabular-nums text-mist">
                      {operation.duration_ms > 0
                        ? formatDuration(operation.duration_ms)
                        : "—"}
                    </td>
                    <td className="max-w-[320px] whitespace-pre-wrap break-words px-4 py-3 text-xs text-coral-red">
                      {operation.error ?? "—"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      ) : null}
    </AdminShell>
  );
}
