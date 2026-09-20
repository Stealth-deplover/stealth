"use client";

import { useMemo, useState } from "react";
import {
  PathsV1AdminInfrastructureMetricsGetParametersQueryScope,
  type components,
} from "@/api/generated/schema";
import { useAdminInfrastructure } from "@/api/queries";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
import { AdminShell } from "./admin-shell";
import { AdminTimeRange, useAdminTimeRange } from "./admin-time-range";

type Scope = PathsV1AdminInfrastructureMetricsGetParametersQueryScope;

const scopes: Array<{ value: Scope; label: string }> = [
  {
    value: PathsV1AdminInfrastructureMetricsGetParametersQueryScope.host,
    label: "Host",
  },
  {
    value: PathsV1AdminInfrastructureMetricsGetParametersQueryScope.containers,
    label: "Containers",
  },
  {
    value: PathsV1AdminInfrastructureMetricsGetParametersQueryScope.postgres,
    label: "PostgreSQL",
  },
  {
    value: PathsV1AdminInfrastructureMetricsGetParametersQueryScope.redis,
    label: "Redis",
  },
  {
    value: PathsV1AdminInfrastructureMetricsGetParametersQueryScope.services,
    label: "Services",
  },
];

export function AdminInfrastructureView() {
  const timeRange = useAdminTimeRange();
  const [scope, setScope] = useState<Scope>(
    PathsV1AdminInfrastructureMetricsGetParametersQueryScope.host,
  );
  const metrics = useAdminInfrastructure(
    { ...timeRange.query, scope, limit: 500 },
    { refetchInterval: timeRange.refreshInterval },
  );
  const items = metrics.data?.items;

  const latest = useMemo(() => {
    const byMetric = new Map<
      string,
      components["schemas"]["AdminInfrastructureMetric"]
    >();
    for (const item of items ?? []) {
      const identity = `${item.service}\u0000${item.name}`;
      const previous = byMetric.get(identity);
      if (!previous || item.timestamp > previous.timestamp) {
        byMetric.set(identity, item);
      }
    }
    return [...byMetric.values()].sort((left, right) =>
      `${left.service}/${left.name}`.localeCompare(
        `${right.service}/${right.name}`,
      ),
    );
  }, [items]);

  return (
    <AdminShell>
      <PageHeader
        eyebrow="Admin / Infrastructure"
        title="Infrastructure"
        description="Collected host, container, database, cache, and service metrics from the private OTel pipeline. Missing data stays visible as missing."
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
      <div
        className="mb-4 flex flex-wrap gap-2"
        role="group"
        aria-label="Infrastructure scope"
      >
        {scopes.map((item) => (
          <button
            key={item.value}
            type="button"
            aria-pressed={scope === item.value}
            onClick={() => setScope(item.value)}
            className={`min-h-11 rounded-md border px-3 py-2 text-sm transition-colors duration-150 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-acid-lime/60 ${scope === item.value ? "border-smoke bg-white/[0.08] text-paper" : "border-graphite text-fog hover:border-smoke hover:text-mist"}`}
          >
            {item.label}
          </button>
        ))}
      </div>
      {metrics.isPending ? <LoadingState rows={7} /> : null}
      {metrics.error ? (
        <ErrorState
          title="Could not load infrastructure metrics"
          error={metrics.error}
          retry={() => metrics.refetch()}
        />
      ) : null}
      {metrics.data && !latest.length ? (
        <Card>
          <CardContent className="p-8 text-center text-sm text-fog">
            No {scope} metrics were collected in this time window.
          </CardContent>
        </Card>
      ) : null}
      {latest.length ? (
        <Card className="overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[820px] text-left text-sm">
              <thead className="border-b border-graphite bg-white/[0.02] text-xs uppercase tracking-[0.1em] text-fog">
                <tr>
                  <th className="px-4 py-3 font-medium">Service</th>
                  <th className="px-4 py-3 font-medium">Metric</th>
                  <th className="px-4 py-3 font-medium">Value</th>
                  <th className="px-4 py-3 font-medium">Collected</th>
                  <th className="px-4 py-3 font-medium">Attributes</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-graphite">
                {latest.map((item) => (
                  <tr
                    key={`${item.service}-${item.name}`}
                    className="hover:bg-white/[0.025]"
                  >
                    <td className="px-4 py-3 text-mist">
                      {item.service || "unknown"}
                    </td>
                    <td className="px-4 py-3 font-mono text-xs text-mist">
                      {item.name}
                    </td>
                    <td className="px-4 py-3">
                      <MetricValue name={item.name} value={item.value} />
                    </td>
                    <td className="whitespace-nowrap px-4 py-3 font-mono text-xs text-fog">
                      {formatDate(item.timestamp)}
                    </td>
                    <td className="max-w-[280px] px-4 py-3">
                      <div className="flex flex-wrap gap-1">
                        {Object.entries({
                          ...item.resource_attributes,
                          ...item.attributes,
                        })
                          .slice(0, 5)
                          .map(([key, value]) => (
                            <Badge key={key} variant="neutral">
                              {key}={value}
                            </Badge>
                          ))}
                      </div>
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

function MetricValue({ name, value }: { name: string; value: number }) {
  const isRatio = name.endsWith(".utilization");
  const formatted = isRatio
    ? `${(value * 100).toFixed(1)}%`
    : value.toLocaleString(undefined, { maximumFractionDigits: 3 });
  return (
    <span className="font-mono text-xs tabular-nums text-paper">
      {formatted}
    </span>
  );
}
