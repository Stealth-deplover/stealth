"use client";

import { useMemo, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { BarChart3, LayoutDashboard, Plus, Trash2 } from "lucide-react";
import {
  useCreateAdminDashboard,
  useDeleteAdminDashboard,
} from "@/api/mutations";
import {
  useAdminDashboard,
  useAdminDashboards,
  useAdminLogs,
  useAdminMetrics,
  useAdminMonitors,
} from "@/api/queries";
import type { components } from "@/api/generated/schema";
import { ErrorState, errorMessage } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { formatDate } from "@/lib/format";
import { AdminMetricChart } from "./admin-metric-chart";
import { AdminShell } from "./admin-shell";
import { AdminTimeRange, useAdminTimeRange } from "./admin-time-range";

type DashboardRequest = components["schemas"]["CreateAdminDashboardRequest"];
type Panel = {
  id?: string;
  type: string;
  title?: string;
  metric?: string;
  service?: string;
  level?: string;
  query?: string;
};

export function AdminDashboardsView() {
  const timeRange = useAdminTimeRange();
  const dashboards = useAdminDashboards({ limit: 100 });
  const [dialogOpen, setDialogOpen] = useState(false);
  const [selectedId, setSelectedId] = useState<string>();
  const remove = useDeleteAdminDashboard();
  const detail = useAdminDashboard(selectedId);
  const selectedPanels = useMemo(
    () => panelsFromDefinition(detail.data?.dashboard.definition),
    [detail.data?.dashboard.definition],
  );

  return (
    <AdminShell>
      <PageHeader
        eyebrow="Admin / Telemetry / Dashboards"
        title="Dashboards"
        description="Saved, owner-defined views backed by bounded telemetry queries. A panel never sends raw SQL to ClickHouse."
        actions={
          <div className="flex flex-wrap items-center gap-3">
            <AdminTimeRange
              rangeKey={timeRange.rangeKey}
              refreshKey={timeRange.refreshKey}
              onRangeChange={timeRange.setRange}
              onRefreshChange={timeRange.setRefresh}
            />
            <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
              <DialogTrigger asChild>
                <Button size="sm">
                  <Plus className="size-3.5" aria-hidden="true" /> New dashboard
                </Button>
              </DialogTrigger>
              <CreateDashboardDialog
                onCreated={(id) => {
                  setDialogOpen(false);
                  setSelectedId(id);
                }}
              />
            </Dialog>
          </div>
        }
      />
      <div className="grid gap-4 lg:grid-cols-[280px_minmax(0,1fr)]">
        <div className="overflow-hidden rounded-xl border border-graphite bg-carbon">
          <div className="border-b border-graphite px-4 py-3">
            <p className="text-xs uppercase tracking-[0.1em] text-fog">
              Saved views
            </p>
          </div>
          {dashboards.isPending ? (
            <div className="p-4">
              <LoadingState rows={3} />
            </div>
          ) : null}
          {dashboards.error ? (
            <div className="p-4">
              <ErrorState
                error={dashboards.error}
                retry={() => dashboards.refetch()}
              />
            </div>
          ) : null}
          {dashboards.data?.items.length ? (
            <div className="divide-y divide-graphite">
              {dashboards.data.items.map((dashboard) => (
                <button
                  key={dashboard.id}
                  type="button"
                  onClick={() => setSelectedId(dashboard.id)}
                  className={`flex w-full items-start gap-3 px-4 py-4 text-left transition-colors duration-150 hover:bg-white/[0.025] ${selectedId === dashboard.id ? "bg-white/[0.05]" : ""}`}
                >
                  <LayoutDashboard
                    className="mt-0.5 size-4 shrink-0 text-fog"
                    aria-hidden="true"
                  />
                  <span className="min-w-0">
                    <span className="block truncate text-sm text-mist">
                      {dashboard.name}
                    </span>
                    <span className="mt-1 block truncate text-xs text-fog">
                      Updated {formatDate(dashboard.updated_at)}
                    </span>
                  </span>
                </button>
              ))}
            </div>
          ) : null}
          {dashboards.data && !dashboards.data.items.length ? (
            <p className="p-5 text-sm leading-6 text-fog">
              No saved dashboards yet.
            </p>
          ) : null}
        </div>
        <div className="min-w-0">
          {!selectedId ? (
            <EmptyDashboard onAdd={() => setDialogOpen(true)} />
          ) : null}
          {selectedId && detail.isPending ? <LoadingState rows={3} /> : null}
          {selectedId && detail.error ? (
            <ErrorState
              title="Could not load dashboard"
              error={detail.error}
              retry={() => detail.refetch()}
            />
          ) : null}
          {detail.data ? (
            <div className="space-y-4">
              <div className="flex flex-wrap items-start justify-between gap-4">
                <div>
                  <p className="text-xs uppercase tracking-[0.1em] text-fog">
                    Dashboard
                  </p>
                  <h2 className="mt-2 text-xl tracking-[-0.012em] text-paper">
                    {detail.data.dashboard.name}
                  </h2>
                  {detail.data.dashboard.description ? (
                    <p className="mt-1 text-sm text-fog">
                      {detail.data.dashboard.description}
                    </p>
                  ) : null}
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  disabled={remove.isPending}
                  onClick={() => {
                    if (
                      window.confirm(
                        `Delete the dashboard “${detail.data?.dashboard.name}”?`,
                      )
                    ) {
                      remove.mutate(detail.data!.dashboard.id, {
                        onSuccess: () => setSelectedId(undefined),
                      });
                    }
                  }}
                >
                  <Trash2 className="size-3.5" aria-hidden="true" /> Delete
                </Button>
              </div>
              {selectedPanels.length ? (
                <div className="grid gap-4 md:grid-cols-2">
                  {selectedPanels.map((panel) => (
                    <DashboardPanel
                      key={panel.id ?? `${panel.type}-${panel.title}`}
                      panel={panel}
                      query={timeRange.query}
                      refreshInterval={timeRange.refreshInterval}
                    />
                  ))}
                </div>
              ) : (
                <div className="rounded-xl border border-dashed border-graphite p-8 text-center text-sm text-fog">
                  This dashboard has no panels.
                </div>
              )}
              {remove.error ? (
                <p className="text-sm text-coral-red" role="alert">
                  {errorMessage(remove.error)}
                </p>
              ) : null}
            </div>
          ) : null}
        </div>
      </div>
    </AdminShell>
  );
}

function CreateDashboardDialog({
  onCreated,
}: {
  onCreated: (id: string) => void;
}) {
  const mutation = useCreateAdminDashboard();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [metric, setMetric] = useState("stealth_api_http_requests_total");
  const [service, setService] = useState("stealth-api");
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const body: DashboardRequest = {
      name,
      description,
      definition: {
        panels: [
          {
            id: "primary",
            type: "time_series",
            title: metric,
            metric,
            service,
          },
        ],
      },
    };
    mutation.mutate(body, {
      onSuccess: (response) => {
        if (response?.dashboard.id) onCreated(response.dashboard.id);
      },
    });
  }
  return (
    <DialogContent>
      <DialogHeader>
        <DialogTitle>New dashboard</DialogTitle>
        <DialogDescription>
          Choose a real metric series for the first panel. More panels can be
          added through the saved definition API.
        </DialogDescription>
      </DialogHeader>
      <form className="space-y-4" onSubmit={submit}>
        <Field label="Name" htmlFor="dashboard-name">
          <Input
            id="dashboard-name"
            required
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="Instance health"
          />
        </Field>
        <Field label="Description" htmlFor="dashboard-description">
          <Textarea
            id="dashboard-description"
            value={description}
            onChange={(event) => setDescription(event.target.value)}
          />
        </Field>
        <Field
          label="Metric name"
          htmlFor="dashboard-metric"
          hint="Use the exact OTel/Prometheus metric name emitted by this instance."
        >
          <Input
            id="dashboard-metric"
            required
            value={metric}
            onChange={(event) => setMetric(event.target.value)}
          />
        </Field>
        <Field label="Service filter" htmlFor="dashboard-service">
          <Input
            id="dashboard-service"
            value={service}
            onChange={(event) => setService(event.target.value)}
            placeholder="Optional"
          />
        </Field>
        {mutation.error ? (
          <p className="text-sm text-coral-red" role="alert">
            {errorMessage(mutation.error)}
          </p>
        ) : null}
        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="secondary">
              Cancel
            </Button>
          </DialogClose>
          <Button type="submit" disabled={mutation.isPending}>
            {mutation.isPending ? "Creating…" : "Create dashboard"}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  );
}

function DashboardPanel({
  panel,
  query,
  refreshInterval,
}: {
  panel: Panel;
  query: { from: string; to: string };
  refreshInterval: number | false;
}) {
  if (panel.type === "monitor_status")
    return <MonitorStatusPanel refreshInterval={refreshInterval} />;
  if (panel.type === "logs")
    return (
      <LogPanel panel={panel} query={query} refreshInterval={refreshInterval} />
    );
  return (
    <MetricPanel
      panel={panel}
      query={query}
      refreshInterval={refreshInterval}
    />
  );
}

function MetricPanel({
  panel,
  query,
  refreshInterval,
}: {
  panel: Panel;
  query: { from: string; to: string };
  refreshInterval: number | false;
}) {
  const metrics = useAdminMetrics(
    { ...query, name: panel.metric, service: panel.service, limit: 1000 },
    { refetchInterval: refreshInterval },
  );
  return (
    <PanelFrame title={panel.title ?? panel.metric ?? "Metrics"}>
      {metrics.isPending ? <LoadingState rows={2} /> : null}
      {metrics.error ? (
        <p className="text-sm text-fog">Telemetry unavailable.</p>
      ) : null}
      {metrics.data?.items.length ? (
        <AdminMetricChart items={metrics.data.items} />
      ) : null}
      {metrics.data && !metrics.data.items.length ? (
        <p className="py-8 text-center text-sm text-fog">
          No metric points in this window.
        </p>
      ) : null}
    </PanelFrame>
  );
}

function LogPanel({
  panel,
  query,
  refreshInterval,
}: {
  panel: Panel;
  query: { from: string; to: string };
  refreshInterval: number | false;
}) {
  const logs = useAdminLogs(
    {
      ...query,
      service: panel.service,
      level: panel.level,
      query: panel.query,
      limit: 20,
    },
    { refetchInterval: refreshInterval },
  );
  return (
    <PanelFrame title={panel.title ?? "Recent logs"}>
      {logs.isPending ? <LoadingState rows={2} /> : null}
      {logs.error ? (
        <p className="text-sm text-fog">Telemetry unavailable.</p>
      ) : null}
      {logs.data?.items.length ? (
        <div className="divide-y divide-graphite">
          {logs.data.items.slice(0, 8).map((item, index) => (
            <div key={`${item.timestamp}-${index}`} className="py-2 first:pt-0">
              <p className="truncate text-xs text-mist">{item.message}</p>
              <p className="mt-1 font-mono text-[10px] text-fog">
                {formatDate(item.timestamp)} · {item.service}
              </p>
            </div>
          ))}
        </div>
      ) : null}
      {logs.data && !logs.data.items.length ? (
        <p className="py-8 text-center text-sm text-fog">
          No logs in this window.
        </p>
      ) : null}
    </PanelFrame>
  );
}

function MonitorStatusPanel({
  refreshInterval,
}: {
  refreshInterval: number | false;
}) {
  const monitors = useAdminMonitors(
    { limit: 100 },
    { refetchInterval: refreshInterval },
  );
  return (
    <PanelFrame title="Monitor status">
      {monitors.isPending ? <LoadingState rows={2} /> : null}
      {monitors.error ? (
        <p className="text-sm text-fog">Monitor state unavailable.</p>
      ) : null}
      {monitors.data?.items.length ? (
        <div className="space-y-2">
          {monitors.data.items.slice(0, 8).map((monitor) => (
            <div
              key={monitor.id}
              className="flex items-center justify-between gap-3"
            >
              <span className="truncate text-sm text-mist">{monitor.name}</span>
              <StatusBadge
                status={monitor.enabled ? monitor.status : "paused"}
              />
            </div>
          ))}
        </div>
      ) : null}
      {monitors.data && !monitors.data.items.length ? (
        <p className="py-8 text-center text-sm text-fog">
          No monitors configured.
        </p>
      ) : null}
    </PanelFrame>
  );
}

function PanelFrame({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) {
  return (
    <div className="min-w-0 rounded-xl border border-graphite bg-carbon p-5">
      <div className="mb-4 flex items-center gap-2">
        <BarChart3 className="size-4 text-fog" aria-hidden="true" />
        <h3 className="text-sm text-paper">{title}</h3>
      </div>
      {children}
    </div>
  );
}
function panelsFromDefinition(
  definition: Record<string, unknown> | undefined,
): Panel[] {
  if (!definition || !Array.isArray(definition.panels)) return [];
  return definition.panels.filter((value): value is Panel =>
    Boolean(
      value &&
      typeof value === "object" &&
      "type" in value &&
      typeof value.type === "string",
    ),
  );
}
function Field({
  label,
  htmlFor,
  hint,
  children,
}: {
  label: string;
  htmlFor: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
      {hint ? <p className="text-[11px] text-fog">{hint}</p> : null}
    </div>
  );
}
function EmptyDashboard({ onAdd }: { onAdd: () => void }) {
  return (
    <div className="rounded-xl border border-dashed border-graphite bg-carbon/50 p-10 text-center">
      <LayoutDashboard className="mx-auto size-5 text-fog" aria-hidden="true" />
      <h2 className="mt-4 text-sm text-paper">Choose a saved dashboard</h2>
      <p className="mt-2 text-sm text-fog">
        Dashboards query the authenticated telemetry API and show an explicit
        empty state when no signal has arrived.
      </p>
      <Button className="mt-5" size="sm" onClick={onAdd}>
        <Plus className="size-3.5" aria-hidden="true" /> New dashboard
      </Button>
    </div>
  );
}
