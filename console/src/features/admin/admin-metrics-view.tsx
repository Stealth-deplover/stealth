"use client";

import { useMemo, useState } from "react";
import { useAdminMetrics } from "@/api/queries";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { formatDate } from "@/lib/format";
import { AdminShell } from "./admin-shell";
import { AdminMetricChart } from "./admin-metric-chart";
import { AdminTimeRange, useAdminTimeRange } from "./admin-time-range";

export function AdminMetricsView() {
  const timeRange = useAdminTimeRange();
  const [service, setService] = useState("");
  const [name, setName] = useState("");
  const query = useMemo(
    () => ({ ...timeRange.query, service, name, limit: 100 }),
    [name, service, timeRange.query],
  );
  const metrics = useAdminMetrics(query, {
    refetchInterval: timeRange.refreshInterval,
  });

  return (
    <AdminShell>
      <PageHeader
        eyebrow="Admin / Telemetry"
        title="Metrics"
        description="Read OTel metric points from the private telemetry store. Charts use the same bounded query boundary."
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
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="Metric name"
            aria-label="Filter by metric name"
          />
        </CardContent>
      </Card>
      {metrics.isPending ? <LoadingState rows={6} /> : null}
      {metrics.error ? (
        <ErrorState
          title="Could not load metrics"
          error={metrics.error}
          retry={() => metrics.refetch()}
        />
      ) : null}
      {metrics.data && !metrics.data.items.length ? (
        <Card>
          <CardContent className="p-8 text-center text-sm text-fog">
            No metrics matched the selected filters.
          </CardContent>
        </Card>
      ) : null}
      {metrics.data?.items.length ? (
        <div className="space-y-4">
          <Card>
            <CardContent className="p-4">
              <AdminMetricChart items={metrics.data.items} />
            </CardContent>
          </Card>
          <Card className="overflow-hidden">
            <div className="overflow-x-auto">
              <table className="w-full min-w-[760px] text-left text-sm">
                <thead className="border-b border-graphite bg-white/[0.02] text-xs uppercase tracking-[0.1em] text-fog">
                  <tr>
                    <th className="px-4 py-3 font-medium">Time</th>
                    <th className="px-4 py-3 font-medium">Metric</th>
                    <th className="px-4 py-3 font-medium">Service</th>
                    <th className="px-4 py-3 font-medium">Kind</th>
                    <th className="px-4 py-3 font-medium text-right">Value</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-graphite">
                  {metrics.data.items.map((item, index) => (
                    <tr
                      key={`${item.timestamp}-${item.name}-${index}`}
                      className="hover:bg-white/[0.025]"
                    >
                      <td className="whitespace-nowrap px-4 py-3 font-mono text-xs text-fog">
                        {formatDate(item.timestamp)}
                      </td>
                      <td className="px-4 py-3 font-mono text-xs text-mist">
                        {item.name}
                      </td>
                      <td className="px-4 py-3 text-mist">{item.service}</td>
                      <td className="px-4 py-3">
                        <Badge variant="neutral">{item.kind}</Badge>
                      </td>
                      <td className="px-4 py-3 text-right font-mono text-xs tabular-nums text-mist">
                        {item.value.toLocaleString(undefined, {
                          maximumFractionDigits: 4,
                        })}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </Card>
        </div>
      ) : null}
    </AdminShell>
  );
}
