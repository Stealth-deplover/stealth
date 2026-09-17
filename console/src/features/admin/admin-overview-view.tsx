"use client";

import Link from "next/link";
import {
  Activity,
  ArrowUpRight,
  Database,
  Server,
  ShieldAlert,
} from "lucide-react";
import { useAdminOverview, useAdminSources } from "@/api/queries";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
import { AdminShell } from "./admin-shell";
import { AdminTimeRange, useAdminTimeRange } from "./admin-time-range";

export function AdminOverviewView() {
  const timeRange = useAdminTimeRange();
  const overview = useAdminOverview({
    refetchInterval: timeRange.refreshInterval,
  });
  const sources = useAdminSources(
    { ...timeRange.query, limit: 8 },
    { refetchInterval: timeRange.refreshInterval },
  );

  if (overview.isPending) {
    return <LoadingState rows={5} />;
  }
  if (overview.error) {
    return (
      <ErrorState
        title="Could not load instance overview"
        error={overview.error}
        retry={() => overview.refetch()}
      />
    );
  }
  const data = overview.data;
  if (!data) return null;

  return (
    <AdminShell>
      <PageHeader
        eyebrow="Admin / Overview"
        title="Instance control room"
        description="Live control-plane health and the telemetry sources currently reporting into this Stealth instance."
        actions={
          <AdminTimeRange
            rangeKey={timeRange.rangeKey}
            refreshKey={timeRange.refreshKey}
            onRangeChange={timeRange.setRange}
            onRefreshChange={timeRange.setRefresh}
          />
        }
      />
      <div className="mb-4 flex items-center gap-3">
        <StatusBadge status={data.instance_status} />
        <span className="text-xs text-fog">
          Checked {formatDate(data.checked_at)}
        </span>
        <Badge
          variant={data.telemetry.status === "healthy" ? "success" : "warning"}
        >
          Telemetry {data.telemetry.status}
        </Badge>
      </div>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {data.components.map((component) => (
          <Card key={component.name}>
            <CardContent className="p-5">
              <div className="flex items-center justify-between gap-3">
                <p className="text-xs uppercase tracking-[0.12em] text-fog">
                  {component.name}
                </p>
                {component.name === "api" ? (
                  <Server className="size-4 text-fog" aria-hidden="true" />
                ) : component.name === "postgres" ? (
                  <Database className="size-4 text-fog" aria-hidden="true" />
                ) : (
                  <Activity className="size-4 text-fog" aria-hidden="true" />
                )}
              </div>
              <div className="mt-4">
                <StatusBadge status={component.status} />
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
      <div className="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {data.operations ? (
          <>
            <OverviewStat
              label="Active deployments"
              value={data.operations.active_deployments}
              href="/admin/operations"
            />
            <OverviewStat
              label="Queued jobs"
              value={data.operations.queued_jobs}
              href="/admin/operations"
            />
            <OverviewStat
              label="Running jobs"
              value={data.operations.running_jobs}
              href="/admin/operations"
            />
            <OverviewStat
              label="Failed jobs"
              value={data.operations.failed_jobs}
              href="/admin/operations"
              warning={data.operations.failed_jobs > 0}
            />
          </>
        ) : (
          <Card className="sm:col-span-2 lg:col-span-4">
            <CardContent className="p-4 text-sm text-fog">
              Durable operation counts are temporarily unavailable.
            </CardContent>
          </Card>
        )}
      </div>
      <div className="mt-4 grid gap-4 lg:grid-cols-[1.3fr_0.7fr]">
        <Card>
          <CardHeader className="flex-row items-center justify-between">
            <div>
              <CardTitle>Reporting sources</CardTitle>
              <p className="mt-1 text-sm text-fog">
                Services observed in the selected window.
              </p>
            </div>
            <Link
              href="/admin/telemetry/sources"
              className="inline-flex items-center gap-1 text-xs text-mist hover:text-paper"
            >
              View all <ArrowUpRight className="size-3.5" aria-hidden="true" />
            </Link>
          </CardHeader>
          <CardContent>
            {sources.isPending ? <LoadingState rows={2} /> : null}
            {sources.error ? (
              <p className="text-sm text-fog">Telemetry sources unavailable.</p>
            ) : null}
            {!sources.isPending &&
            !sources.error &&
            !sources.data?.items.length ? (
              <div className="flex items-center gap-3 rounded-md border border-dashed border-graphite px-4 py-6 text-sm text-fog">
                <ShieldAlert className="size-4" aria-hidden="true" />
                No telemetry has arrived in this time window.
              </div>
            ) : null}
            {sources.data?.items.length ? (
              <div className="divide-y divide-graphite">
                {sources.data.items.map((source) => (
                  <div
                    key={`${source.service}-${source.signal}`}
                    className="flex items-center justify-between gap-4 py-3 first:pt-0 last:pb-0"
                  >
                    <div className="min-w-0">
                      <p className="truncate text-sm text-mist">
                        {source.service}
                      </p>
                      <p className="mt-1 text-xs text-fog">{source.signal}</p>
                    </div>
                    <div className="shrink-0 text-right">
                      <p className="font-mono text-xs tabular-nums text-mist">
                        {source.volume.toLocaleString()}
                      </p>
                      <p className="mt-1 text-[11px] text-fog">
                        {formatDate(source.last_received)}
                      </p>
                    </div>
                  </div>
                ))}
              </div>
            ) : null}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Telemetry boundary</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3 text-sm leading-6 text-fog">
            <p>
              The browser talks only to the authenticated Stealth API.
              ClickHouse and the Collector remain private services.
            </p>
            <p>
              If telemetry is unavailable, deployment and project control-plane
              functions continue independently.
            </p>
          </CardContent>
        </Card>
      </div>
    </AdminShell>
  );
}

function OverviewStat({
  label,
  value,
  href,
  warning = false,
}: {
  label: string;
  value: number;
  href: string;
  warning?: boolean;
}) {
  return (
    <Link href={href} className="block">
      <Card className="h-full transition-colors duration-150 hover:border-smoke">
        <CardContent className="p-5">
          <p className="text-xs uppercase tracking-[0.12em] text-fog">
            {label}
          </p>
          <p
            className={`mt-3 font-mono text-2xl tabular-nums ${warning ? "text-coral-red" : "text-paper"}`}
          >
            {value.toLocaleString()}
          </p>
        </CardContent>
      </Card>
    </Link>
  );
}
