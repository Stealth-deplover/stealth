"use client";

import { useMemo, useState } from "react";
import { useSearchParams } from "next/navigation";
import { useAdminTraces } from "@/api/queries";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { formatDate, formatDuration } from "@/lib/format";
import { AdminShell } from "./admin-shell";
import { AdminTimeRange, useAdminTimeRange } from "./admin-time-range";

export function AdminTracesView() {
  const timeRange = useAdminTimeRange();
  const searchParams = useSearchParams();
  const [service, setService] = useState("");
  const [traceID, setTraceID] = useState(
    () => searchParams.get("trace_id") ?? "",
  );
  const query = useMemo(
    () => ({ ...timeRange.query, service, trace_id: traceID, limit: 100 }),
    [service, timeRange.query, traceID],
  );
  const traces = useAdminTraces(query, {
    refetchInterval: timeRange.refreshInterval,
  });

  return (
    <AdminShell>
      <PageHeader
        eyebrow="Admin / Telemetry"
        title="Traces"
        description="Inspect spans emitted by the API, worker, and internal operations."
        actions={
          <AdminTimeRange
            rangeKey={timeRange.rangeKey}
            refreshKey={timeRange.refreshKey}
            onRangeChange={timeRange.setRange}
            onRefreshChange={timeRange.setRefresh}
          />
        }
      />
      <Card className="mb-4">
        <CardContent className="grid gap-3 p-4 md:grid-cols-2">
          <Input
            value={service}
            onChange={(event) => setService(event.target.value)}
            placeholder="Service"
            aria-label="Filter by service"
          />
          <Input
            value={traceID}
            onChange={(event) => setTraceID(event.target.value)}
            placeholder="Trace ID"
            aria-label="Filter by trace ID"
          />
        </CardContent>
      </Card>
      {traces.isPending ? <LoadingState rows={6} /> : null}
      {traces.error ? (
        <ErrorState
          title="Could not load traces"
          error={traces.error}
          retry={() => traces.refetch()}
        />
      ) : null}
      {traces.data && !traces.data.items.length ? (
        <Card>
          <CardContent className="p-8 text-center text-sm text-fog">
            No spans matched the selected filters.
          </CardContent>
        </Card>
      ) : null}
      {traces.data?.items.length ? (
        <Card className="overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[900px] text-left text-sm">
              <thead className="border-b border-graphite bg-white/[0.02] text-xs uppercase tracking-[0.1em] text-fog">
                <tr>
                  <th className="px-4 py-3 font-medium">Time</th>
                  <th className="px-4 py-3 font-medium">Service</th>
                  <th className="px-4 py-3 font-medium">Operation</th>
                  <th className="px-4 py-3 font-medium">Duration</th>
                  <th className="px-4 py-3 font-medium">Status</th>
                  <th className="px-4 py-3 font-medium">Trace ID</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-graphite">
                {traces.data.items.map((item, index) => (
                  <tr
                    key={`${item.span_id}-${index}`}
                    className="hover:bg-white/[0.025]"
                  >
                    <td className="whitespace-nowrap px-4 py-3 font-mono text-xs text-fog">
                      {formatDate(item.timestamp)}
                    </td>
                    <td className="px-4 py-3 text-mist">{item.service}</td>
                    <td
                      className="max-w-[300px] truncate px-4 py-3 text-mist"
                      title={item.name}
                    >
                      {item.name}
                      <span className="ml-2 text-xs text-fog">{item.kind}</span>
                    </td>
                    <td className="whitespace-nowrap px-4 py-3 font-mono text-xs text-mist">
                      {formatDuration(item.duration_ns / 1_000_000)}
                    </td>
                    <td className="px-4 py-3">
                      <StatusBadge status={item.status} />
                    </td>
                    <td className="px-4 py-3">
                      <Badge
                        variant="neutral"
                        className="font-mono text-[10px]"
                      >
                        {item.trace_id}
                      </Badge>
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
