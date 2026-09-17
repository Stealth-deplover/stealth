"use client";

import { useMemo, useState } from "react";
import { Search } from "lucide-react";
import { useAdminLogs } from "@/api/queries";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { formatDate } from "@/lib/format";
import { AdminShell } from "./admin-shell";
import { AdminTimeRange, useAdminTimeRange } from "./admin-time-range";

export function AdminLogsView() {
  const timeRange = useAdminTimeRange();
  const [search, setSearch] = useState("");
  const [service, setService] = useState("");
  const [level, setLevel] = useState("");
  const query = useMemo(
    () => ({ ...timeRange.query, query: search, service, level, limit: 100 }),
    [level, search, service, timeRange.query],
  );
  const logs = useAdminLogs(query, {
    refetchInterval: timeRange.refreshInterval,
  });

  return (
    <AdminShell>
      <PageHeader
        eyebrow="Admin / Telemetry"
        title="Logs"
        description="Search structured logs collected from the Stealth runtime and its Docker services."
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
        <CardContent className="grid gap-3 p-4 md:grid-cols-[1fr_0.35fr_0.25fr]">
          <label className="relative block">
            <span className="sr-only">Search logs</span>
            <Search
              className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-ash"
              aria-hidden="true"
            />
            <Input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Search message text"
              className="pl-9"
            />
          </label>
          <label>
            <span className="sr-only">Service</span>
            <Input
              value={service}
              onChange={(event) => setService(event.target.value)}
              placeholder="Service"
            />
          </label>
          <label>
            <span className="sr-only">Level</span>
            <Input
              value={level}
              onChange={(event) => setLevel(event.target.value)}
              placeholder="Level"
            />
          </label>
        </CardContent>
      </Card>
      {logs.isPending ? <LoadingState rows={6} /> : null}
      {logs.error ? (
        <ErrorState
          title="Could not load logs"
          error={logs.error}
          retry={() => logs.refetch()}
        />
      ) : null}
      {logs.data && !logs.data.items.length ? (
        <Card>
          <CardContent className="p-8 text-center text-sm text-fog">
            No logs matched the selected filters.
          </CardContent>
        </Card>
      ) : null}
      {logs.data?.items.length ? (
        <Card className="overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[760px] text-left text-sm">
              <thead className="border-b border-graphite bg-white/[0.02] text-xs uppercase tracking-[0.1em] text-fog">
                <tr>
                  <th className="px-4 py-3 font-medium">Time</th>
                  <th className="px-4 py-3 font-medium">Service</th>
                  <th className="px-4 py-3 font-medium">Level</th>
                  <th className="px-4 py-3 font-medium">Message</th>
                  <th className="px-4 py-3 font-medium">Trace</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-graphite">
                {logs.data.items.map((item, index) => (
                  <tr
                    key={`${item.timestamp}-${item.trace_id}-${index}`}
                    className="align-top hover:bg-white/[0.025]"
                  >
                    <td className="whitespace-nowrap px-4 py-3 font-mono text-xs text-fog">
                      {formatDate(item.timestamp)}
                    </td>
                    <td className="px-4 py-3 text-mist">{item.service}</td>
                    <td className="px-4 py-3">
                      <Badge variant="neutral">{item.level ?? "—"}</Badge>
                    </td>
                    <td className="max-w-[520px] whitespace-pre-wrap break-words px-4 py-3 text-mist">
                      {item.message}
                    </td>
                    <td className="px-4 py-3 font-mono text-xs text-fog">
                      {item.trace_id ?? "—"}
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
