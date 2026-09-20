"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import type { components } from "@/api/generated/schema";
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

type TraceSpan = components["schemas"]["AdminTraceSpan"];

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
            customRange={timeRange.customRange}
            onRangeChange={timeRange.setRange}
            onRefreshChange={timeRange.setRefresh}
            onCustomRangeChange={timeRange.setCustomRange}
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
      {traceID && traces.data?.items.length ? (
        <TraceDetail spans={traces.data.items} />
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
                      <button
                        type="button"
                        onClick={() => setTraceID(item.trace_id)}
                        className="rounded border border-transparent focus-visible:border-acid-lime/70 focus-visible:outline-none"
                        aria-label={`Open trace ${item.trace_id}`}
                      >
                        <Badge
                          variant="neutral"
                          className="font-mono text-[10px]"
                        >
                          {item.trace_id}
                        </Badge>
                      </button>
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

function TraceDetail({ spans }: { spans: TraceSpan[] }) {
  const rows = useMemo(() => flattenTrace(spans), [spans]);
  const start = Math.min(
    ...spans.map((span) => new Date(span.timestamp).getTime()),
  );
  const end = Math.max(
    ...spans.map(
      (span) => new Date(span.timestamp).getTime() + span.duration_ns / 1e6,
    ),
  );
  const duration = Math.max(end - start, 1);

  return (
    <Card className="mb-4">
      <CardContent className="space-y-4 p-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <p className="text-xs uppercase tracking-[0.1em] text-fog">
              Trace detail
            </p>
            <p className="mt-1 text-sm text-mist">
              {spans.length} span{spans.length === 1 ? "" : "s"} in the selected
              trace.
            </p>
          </div>
          <Link
            href={`/admin/telemetry/logs?trace_id=${encodeURIComponent(spans[0]?.trace_id ?? "")}`}
            className="text-xs text-mist underline decoration-graphite underline-offset-4 hover:text-paper"
          >
            View linked logs
          </Link>
        </div>
        <div className="space-y-2" role="list" aria-label="Trace waterfall">
          {rows.map(({ span, depth }) => {
            const left =
              ((new Date(span.timestamp).getTime() - start) / duration) * 100;
            const width = Math.max(
              (span.duration_ns / 1e6 / duration) * 100,
              0.5,
            );
            return (
              <div
                key={span.span_id}
                role="listitem"
                className="grid gap-2 text-xs sm:grid-cols-[minmax(180px,0.7fr)_minmax(220px,1fr)_90px] sm:items-center"
              >
                <div
                  className="min-w-0 truncate text-mist"
                  style={{ paddingLeft: `${depth * 16}px` }}
                  title={span.name}
                >
                  <span className="text-fog">{span.service}</span>
                  <span className="mx-1 text-ash">/</span>
                  {span.name}
                </div>
                <div className="relative h-6 overflow-hidden rounded border border-graphite bg-void">
                  <div
                    className={`absolute top-1 h-4 rounded-sm ${span.status.toUpperCase() === "ERROR" ? "bg-coral-red/80" : "bg-signal-teal/70"}`}
                    style={{
                      left: `${Math.min(left, 99.5)}%`,
                      width: `${Math.min(width, 100 - Math.min(left, 99.5))}%`,
                    }}
                    title={`${span.name}: ${formatDuration(span.duration_ns / 1e6)}`}
                  />
                </div>
                <div className="font-mono tabular-nums text-fog">
                  {formatDuration(span.duration_ns / 1e6)}
                </div>
              </div>
            );
          })}
        </div>
      </CardContent>
    </Card>
  );
}

function flattenTrace(spans: TraceSpan[]) {
  const byParent = new Map<string, TraceSpan[]>();
  const byID = new Set(spans.map((span) => span.span_id));
  for (const span of spans) {
    const parent =
      span.parent_span_id && byID.has(span.parent_span_id)
        ? span.parent_span_id
        : "";
    const children = byParent.get(parent) ?? [];
    children.push(span);
    byParent.set(parent, children);
  }
  for (const children of byParent.values()) {
    children.sort((left, right) =>
      left.timestamp.localeCompare(right.timestamp),
    );
  }
  const rows: Array<{ span: TraceSpan; depth: number }> = [];
  const visited = new Set<string>();
  const visit = (span: TraceSpan, depth: number) => {
    if (visited.has(span.span_id)) return;
    visited.add(span.span_id);
    rows.push({ span, depth });
    for (const child of byParent.get(span.span_id) ?? [])
      visit(child, depth + 1);
  };
  for (const root of byParent.get("") ?? []) visit(root, 0);
  for (const span of spans) visit(span, 0);
  return rows;
}
