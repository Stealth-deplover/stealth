"use client";

import Link from "next/link";
import { AlertTriangle } from "lucide-react";
import { useMemo, useState } from "react";
import { useAdminErrors, useAdminLogVolume } from "@/api/queries";
import type { components } from "@/api/generated/schema";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { formatDate } from "@/lib/format";
import { AdminLogVolumeChart } from "./admin-log-volume-chart";
import { AdminShell } from "./admin-shell";
import { AdminTimeRange, useAdminTimeRange } from "./admin-time-range";

type ErrorGroup = components["schemas"]["AdminErrorGroup"];

export function AdminErrorsView() {
  const timeRange = useAdminTimeRange();
  const [service, setService] = useState("");
  const [search, setSearch] = useState("");
  const query = useMemo(
    () => ({ ...timeRange.query, service, query: search, limit: 100 }),
    [search, service, timeRange.query],
  );
  const errors = useAdminErrors(query, {
    refetchInterval: timeRange.refreshInterval,
  });
  const volume = useAdminLogVolume(query, {
    refetchInterval: timeRange.refreshInterval,
  });

  return (
    <AdminShell>
      <PageHeader
        eyebrow="Admin / Telemetry"
        title="Errors"
        description="Grouped error log occurrences from the selected window. The grouping fingerprint is derived from redacted service, level, and message fields."
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
        <CardContent className="grid gap-3 p-4 sm:grid-cols-2">
          <Input
            value={service}
            onChange={(event) => setService(event.target.value)}
            placeholder="Service"
            aria-label="Filter errors by service"
          />
          <Input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Search message text"
            aria-label="Search error messages"
          />
        </CardContent>
      </Card>
      {volume.error ? (
        <ErrorState
          title="Could not load log volume"
          error={volume.error}
          retry={() => volume.refetch()}
        />
      ) : null}
      {volume.isPending ? <LoadingState rows={2} /> : null}
      {volume.data?.items.length ? (
        <Card className="mb-4">
          <CardHeader>
            <CardTitle>Log volume</CardTitle>
          </CardHeader>
          <CardContent>
            <AdminLogVolumeChart items={volume.data.items} />
          </CardContent>
        </Card>
      ) : null}
      {errors.isPending ? <LoadingState rows={5} /> : null}
      {errors.error ? (
        <ErrorState
          title="Could not load error groups"
          error={errors.error}
          retry={() => errors.refetch()}
        />
      ) : null}
      {errors.data && !errors.data.items.length ? (
        <Card>
          <CardContent className="flex items-center gap-3 p-8 text-sm text-fog">
            <AlertTriangle className="size-4" aria-hidden="true" />
            No grouped errors matched this time window.
          </CardContent>
        </Card>
      ) : null}
      {errors.data?.items.length ? (
        <ErrorGroupTable items={errors.data.items} />
      ) : null}
    </AdminShell>
  );
}

function ErrorGroupTable({ items }: { items: ErrorGroup[] }) {
  return (
    <Card className="overflow-hidden">
      <div className="overflow-x-auto">
        <table className="w-full min-w-[900px] text-left text-sm">
          <thead className="border-b border-graphite bg-white/[0.02] text-xs uppercase tracking-[0.1em] text-fog">
            <tr>
              <th className="px-4 py-3 font-medium">Error</th>
              <th className="px-4 py-3 font-medium">Service</th>
              <th className="px-4 py-3 font-medium">Occurrences</th>
              <th className="px-4 py-3 font-medium">Last seen</th>
              <th className="px-4 py-3 font-medium">Trace</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-graphite">
            {items.map((item) => (
              <tr
                key={item.fingerprint}
                className="align-top hover:bg-white/[0.025]"
              >
                <td className="max-w-[480px] px-4 py-4">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="error">{item.error_type}</Badge>
                    <code className="font-mono text-[10px] text-fog">
                      {item.fingerprint.slice(0, 16)}
                    </code>
                  </div>
                  <p className="mt-2 break-words text-mist">{item.message}</p>
                  <p className="mt-1 text-xs text-fog">
                    First seen {formatDate(item.first_seen)}
                  </p>
                </td>
                <td className="px-4 py-4 text-mist">{item.service}</td>
                <td className="px-4 py-4 font-mono text-xs tabular-nums text-mist">
                  {item.occurrence_count.toLocaleString()}
                </td>
                <td className="whitespace-nowrap px-4 py-4 text-xs text-fog">
                  {formatDate(item.last_seen)}
                </td>
                <td className="px-4 py-4">
                  {item.trace_id ? (
                    <Link
                      href={`/admin/telemetry/traces?trace_id=${encodeURIComponent(item.trace_id)}`}
                      className="font-mono text-[11px] text-mist underline decoration-graphite underline-offset-4 hover:text-paper"
                    >
                      {item.trace_id.slice(0, 16)}
                    </Link>
                  ) : (
                    <span className="text-xs text-fog">none</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  );
}
