"use client";

import { useAdminSources } from "@/api/queries";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
import { AdminShell } from "./admin-shell";
import { AdminTimeRange, useAdminTimeRange } from "./admin-time-range";

export function AdminSourcesView() {
  const timeRange = useAdminTimeRange();
  const sources = useAdminSources({ ...timeRange.query, limit: 100 });
  return (
    <AdminShell>
      <PageHeader
        eyebrow="Admin / Telemetry"
        title="Sources"
        description="Services and signal types observed by the OTel pipeline."
        actions={
          <AdminTimeRange
            rangeKey={timeRange.rangeKey}
            refreshKey={timeRange.refreshKey}
            onRangeChange={timeRange.setRange}
            onRefreshChange={timeRange.setRefresh}
          />
        }
      />
      {sources.isPending ? <LoadingState rows={5} /> : null}
      {sources.error ? (
        <ErrorState
          title="Could not load telemetry sources"
          error={sources.error}
          retry={() => sources.refetch()}
        />
      ) : null}
      {sources.data && !sources.data.items.length ? (
        <Card>
          <CardContent className="p-8 text-center text-sm text-fog">
            No telemetry sources reported in this window.
          </CardContent>
        </Card>
      ) : null}
      {sources.data?.items.length ? (
        <Card className="overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[640px] text-left text-sm">
              <thead className="border-b border-graphite bg-white/[0.02] text-xs uppercase tracking-[0.1em] text-fog">
                <tr>
                  <th className="px-4 py-3 font-medium">Service</th>
                  <th className="px-4 py-3 font-medium">Signal</th>
                  <th className="px-4 py-3 font-medium">Last received</th>
                  <th className="px-4 py-3 text-right font-medium">Volume</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-graphite">
                {sources.data.items.map((item) => (
                  <tr key={`${item.service}-${item.signal}`}>
                    <td className="px-4 py-3 text-mist">{item.service}</td>
                    <td className="px-4 py-3 text-fog">{item.signal}</td>
                    <td className="px-4 py-3 font-mono text-xs text-fog">
                      {formatDate(item.last_received)}
                    </td>
                    <td className="px-4 py-3 text-right font-mono text-xs tabular-nums text-mist">
                      {item.volume.toLocaleString()}
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
