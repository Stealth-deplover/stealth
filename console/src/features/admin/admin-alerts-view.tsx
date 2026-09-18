"use client";

import { useMemo, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { BellRing, Plus, Trash2 } from "lucide-react";
import { useCreateAdminAlert, useDeleteAdminAlert } from "@/api/mutations";
import { useAdminAlerts, useAdminMonitors } from "@/api/queries";
import {
  CreateAdminAlertRuleRequestKind,
  CreateAdminAlertRuleRequestSeverity,
  type components,
} from "@/api/generated/schema";
import { ErrorState, errorMessage } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { Badge, StatusBadge } from "@/components/ui/badge";
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
import { formatDate } from "@/lib/format";
import { AdminShell } from "./admin-shell";
import { AdminTimeRange, useAdminTimeRange } from "./admin-time-range";

type CreateAlertRequest = components["schemas"]["CreateAdminAlertRuleRequest"];

export function AdminAlertsView() {
  const timeRange = useAdminTimeRange();
  const alerts = useAdminAlerts(
    { limit: 100 },
    { refetchInterval: timeRange.refreshInterval },
  );
  const remove = useDeleteAdminAlert();
  const [dialogOpen, setDialogOpen] = useState(false);
  const monitors = useAdminMonitors({ limit: 100 });

  return (
    <AdminShell>
      <PageHeader
        eyebrow="Admin / Alerts"
        title="Alert rules"
        description="Durable rules evaluated by trusted workers. Rules are bounded definitions, never arbitrary ClickHouse queries."
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
                  <Plus className="size-3.5" aria-hidden="true" /> Add rule
                </Button>
              </DialogTrigger>
              <CreateAlertDialog
                monitors={monitors.data?.items ?? []}
                onCreated={() => setDialogOpen(false)}
              />
            </Dialog>
          </div>
        }
      />
      {alerts.isPending ? <LoadingState rows={5} /> : null}
      {alerts.error ? (
        <ErrorState
          title="Could not load alert rules"
          error={alerts.error}
          retry={() => alerts.refetch()}
        />
      ) : null}
      {alerts.data && !alerts.data.items.length ? (
        <div className="rounded-xl border border-dashed border-graphite bg-carbon/50 p-10 text-center">
          <BellRing className="mx-auto size-5 text-fog" aria-hidden="true" />
          <h2 className="mt-4 text-sm text-paper">No alert rules configured</h2>
          <p className="mx-auto mt-2 max-w-md text-sm leading-6 text-fog">
            Start with a monitor failure rule. Its state changes are persisted
            with the monitor check, so a worker restart cannot erase an alert.
          </p>
        </div>
      ) : null}
      {alerts.data?.items.length ? (
        <div className="overflow-hidden rounded-xl border border-graphite bg-carbon">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[880px] text-left text-sm">
              <thead className="border-b border-graphite bg-white/[0.02] text-xs uppercase tracking-[0.1em] text-fog">
                <tr>
                  <th className="px-4 py-3 font-medium">Rule</th>
                  <th className="px-4 py-3 font-medium">Severity</th>
                  <th className="px-4 py-3 font-medium">State</th>
                  <th className="px-4 py-3 font-medium">Last evaluated</th>
                  <th className="px-4 py-3 font-medium">Duration</th>
                  <th className="px-4 py-3 font-medium" aria-label="Actions" />
                </tr>
              </thead>
              <tbody className="divide-y divide-graphite">
                {alerts.data.items.map((rule) => (
                  <tr
                    key={rule.id}
                    className="align-top hover:bg-white/[0.025]"
                  >
                    <td className="px-4 py-4">
                      <p className="text-mist">{rule.name}</p>
                      <p className="mt-1 font-mono text-[11px] text-fog">
                        {rule.kind}
                      </p>
                    </td>
                    <td className="px-4 py-4">
                      <Badge
                        variant={
                          rule.severity === "critical"
                            ? "error"
                            : rule.severity === "warning"
                              ? "warning"
                              : "neutral"
                        }
                      >
                        {rule.severity}
                      </Badge>
                    </td>
                    <td className="px-4 py-4">
                      <StatusBadge
                        status={rule.enabled ? rule.state : "muted"}
                      />
                    </td>
                    <td className="px-4 py-4 text-xs text-fog">
                      {formatDate(rule.last_evaluated_at)}
                    </td>
                    <td className="px-4 py-4 font-mono text-xs text-mist">
                      {rule.for_seconds ? `${rule.for_seconds}s` : "Immediate"}
                    </td>
                    <td className="px-4 py-4 text-right">
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        aria-label={`Delete ${rule.name}`}
                        onClick={() => {
                          if (
                            window.confirm(
                              `Delete the alert rule “${rule.name}”?`,
                            )
                          )
                            remove.mutate(rule.id);
                        }}
                        disabled={remove.isPending}
                      >
                        <Trash2
                          className="size-4 text-fog"
                          aria-hidden="true"
                        />
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {remove.error ? (
            <p
              className="border-t border-graphite p-4 text-sm text-coral-red"
              role="alert"
            >
              {errorMessage(remove.error)}
            </p>
          ) : null}
        </div>
      ) : null}
    </AdminShell>
  );
}

function CreateAlertDialog({
  monitors,
  onCreated,
}: {
  monitors: components["schemas"]["AdminMonitor"][];
  onCreated: () => void;
}) {
  const mutation = useCreateAdminAlert();
  const [name, setName] = useState("");
  const [monitorId, setMonitorId] = useState("");
  const [kind, setKind] = useState<CreateAlertRequest["kind"]>(
    CreateAdminAlertRuleRequestKind.monitor_failure,
  );
  const [severity, setSeverity] = useState<CreateAlertRequest["severity"]>(
    CreateAdminAlertRuleRequestSeverity.warning,
  );
  const [forSeconds, setForSeconds] = useState(0);
  const [certificateDays, setCertificateDays] = useState(7);
  const [operator, setOperator] = useState("gt");
  const [threshold, setThreshold] = useState(0.05);
  const [windowSeconds, setWindowSeconds] = useState(300);
  const [metric, setMetric] = useState("system.cpu.utilization");
  const [service, setService] = useState("");
  const [aggregation, setAggregation] = useState("avg");
  const [percentile, setPercentile] = useState("p95");
  const [search, setSearch] = useState("");
  const [level, setLevel] = useState("ERROR");
  const requiresMonitor =
    kind === CreateAdminAlertRuleRequestKind.monitor_failure ||
    kind === CreateAdminAlertRuleRequestKind.heartbeat_failure ||
    kind === CreateAdminAlertRuleRequestKind.certificate_expiry;
  const availableMonitors = useMemo(
    () =>
      monitors.filter((monitor) =>
        kind === CreateAdminAlertRuleRequestKind.certificate_expiry
          ? monitor.kind === "tls"
          : kind === CreateAdminAlertRuleRequestKind.heartbeat_failure
            ? monitor.kind === "heartbeat"
            : true,
      ),
    [kind, monitors],
  );

  function resetKindFields(nextKind: CreateAlertRequest["kind"]) {
    setKind(nextKind);
    setMonitorId("");
    setThreshold(
      nextKind === CreateAdminAlertRuleRequestKind.disk_pressure ? 0.9 : 0.05,
    );
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const condition = requiresMonitor
      ? kind === CreateAdminAlertRuleRequestKind.certificate_expiry
        ? { monitor_id: monitorId, days: certificateDays }
        : { monitor_id: monitorId }
      : {
          operator,
          threshold,
          window_seconds: windowSeconds,
          ...(kind === CreateAdminAlertRuleRequestKind.metric_threshold ||
          kind === CreateAdminAlertRuleRequestKind.disk_pressure
            ? { metric }
            : {}),
          ...(service ? { service } : {}),
          ...(kind === CreateAdminAlertRuleRequestKind.metric_threshold
            ? { aggregation }
            : {}),
          ...(kind === CreateAdminAlertRuleRequestKind.latency
            ? { percentile }
            : {}),
          ...(kind === CreateAdminAlertRuleRequestKind.log_match
            ? { search, level }
            : {}),
        };
    mutation.mutate(
      {
        name,
        kind,
        condition,
        severity,
        for_seconds: forSeconds,
        enabled: true,
      },
      { onSuccess: onCreated },
    );
  }

  return (
    <DialogContent>
      <DialogHeader>
        <DialogTitle>Add {alertKindLabel(kind).toLowerCase()} rule</DialogTitle>
        <DialogDescription>
          The rule is evaluated in the same transaction as the monitor result
          and becomes firing only after the optional for-duration.
        </DialogDescription>
      </DialogHeader>
      <form className="space-y-4" onSubmit={submit}>
        <Field label="Name" htmlFor="alert-name">
          <Input
            id="alert-name"
            required
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="Public API unavailable"
          />
        </Field>
        <Field label="Rule type" htmlFor="alert-kind">
          <select
            id="alert-kind"
            value={kind}
            onChange={(event) => {
              resetKindFields(event.target.value as CreateAlertRequest["kind"]);
            }}
            className="min-h-11 w-full rounded-md border border-graphite bg-carbon px-3.5 text-sm text-mist outline-none focus:border-acid-lime/70 focus:ring-2 focus:ring-acid-lime/15"
          >
            <option value={CreateAdminAlertRuleRequestKind.metric_threshold}>
              Metric threshold
            </option>
            <option value={CreateAdminAlertRuleRequestKind.error_rate}>
              HTTP error rate
            </option>
            <option value={CreateAdminAlertRuleRequestKind.latency}>
              HTTP latency
            </option>
            <option value={CreateAdminAlertRuleRequestKind.log_match}>
              Log match
            </option>
            <option value={CreateAdminAlertRuleRequestKind.service_health}>
              Service health
            </option>
            <option value={CreateAdminAlertRuleRequestKind.disk_pressure}>
              Disk pressure
            </option>
            <option value={CreateAdminAlertRuleRequestKind.monitor_failure}>
              Monitor failure
            </option>
            <option value={CreateAdminAlertRuleRequestKind.heartbeat_failure}>
              Heartbeat missing
            </option>
            <option value={CreateAdminAlertRuleRequestKind.certificate_expiry}>
              Certificate expiry
            </option>
          </select>
        </Field>
        {requiresMonitor ? (
          <Field label="Monitor" htmlFor="alert-monitor">
            <select
              id="alert-monitor"
              required
              value={monitorId}
              onChange={(event) => setMonitorId(event.target.value)}
              className="min-h-11 w-full rounded-md border border-graphite bg-carbon px-3.5 text-sm text-mist outline-none focus:border-acid-lime/70 focus:ring-2 focus:ring-acid-lime/15"
            >
              <option value="">Select a monitor</option>
              {availableMonitors.map((monitor) => (
                <option key={monitor.id} value={monitor.id}>
                  {monitor.name} · {monitor.kind}
                </option>
              ))}
            </select>
            {!availableMonitors.length ? (
              <p className="mt-1 text-[11px] text-fog">
                {kind === CreateAdminAlertRuleRequestKind.certificate_expiry
                  ? "Create a TLS monitor first."
                  : kind === CreateAdminAlertRuleRequestKind.heartbeat_failure
                    ? "Create a heartbeat monitor first."
                    : "Create a monitor first."}
              </p>
            ) : null}
          </Field>
        ) : null}
        {kind === CreateAdminAlertRuleRequestKind.certificate_expiry ? (
          <Field label="Alert when days remain below" htmlFor="alert-days">
            <Input
              id="alert-days"
              type="number"
              min={1}
              max={3650}
              value={certificateDays}
              onChange={(event) =>
                setCertificateDays(Number(event.target.value))
              }
              required
            />
          </Field>
        ) : null}
        {!requiresMonitor ? (
          <div className="space-y-4 rounded-md border border-graphite bg-void/40 p-3">
            {kind === CreateAdminAlertRuleRequestKind.metric_threshold ? (
              <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_160px]">
                <Field
                  label="Metric name"
                  htmlFor="alert-metric"
                  hint="Exact OTel metric name from the instance."
                >
                  <Input
                    id="alert-metric"
                    required
                    value={metric}
                    onChange={(event) => setMetric(event.target.value)}
                  />
                </Field>
                <Field label="Aggregation" htmlFor="alert-aggregation">
                  <select
                    id="alert-aggregation"
                    value={aggregation}
                    onChange={(event) => setAggregation(event.target.value)}
                    className="min-h-11 w-full rounded-md border border-graphite bg-carbon px-3 text-sm text-mist outline-none focus:border-acid-lime/70"
                  >
                    <option value="avg">Average</option>
                    <option value="latest">Latest</option>
                    <option value="max">Maximum</option>
                    <option value="min">Minimum</option>
                    <option value="sum">Sum</option>
                  </select>
                </Field>
              </div>
            ) : null}
            {kind === CreateAdminAlertRuleRequestKind.disk_pressure ? (
              <Field
                label="Metric name"
                htmlFor="alert-disk-metric"
                hint="Defaults to system.filesystem.utilization."
              >
                <Input
                  id="alert-disk-metric"
                  value={metric}
                  onChange={(event) => setMetric(event.target.value)}
                />
              </Field>
            ) : null}
            {kind === CreateAdminAlertRuleRequestKind.log_match ? (
              <div className="grid gap-4 sm:grid-cols-2">
                <Field label="Search text" htmlFor="alert-search">
                  <Input
                    id="alert-search"
                    required
                    value={search}
                    onChange={(event) => setSearch(event.target.value)}
                    placeholder="database unavailable"
                  />
                </Field>
                <Field label="Level" htmlFor="alert-level">
                  <Input
                    id="alert-level"
                    value={level}
                    onChange={(event) => setLevel(event.target.value)}
                    placeholder="ERROR"
                  />
                </Field>
              </div>
            ) : null}
            {kind === CreateAdminAlertRuleRequestKind.service_health ||
            kind === CreateAdminAlertRuleRequestKind.error_rate ||
            kind === CreateAdminAlertRuleRequestKind.latency ? (
              <Field
                label={
                  kind === CreateAdminAlertRuleRequestKind.service_health
                    ? "Service"
                    : "Service filter"
                }
                htmlFor="alert-service"
              >
                <Input
                  id="alert-service"
                  required={
                    kind === CreateAdminAlertRuleRequestKind.service_health
                  }
                  value={service}
                  onChange={(event) => setService(event.target.value)}
                  placeholder="stealth-api"
                />
              </Field>
            ) : null}
            {kind === CreateAdminAlertRuleRequestKind.latency ? (
              <Field label="Percentile" htmlFor="alert-percentile">
                <select
                  id="alert-percentile"
                  value={percentile}
                  onChange={(event) => setPercentile(event.target.value)}
                  className="min-h-11 w-full rounded-md border border-graphite bg-carbon px-3 text-sm text-mist outline-none focus:border-acid-lime/70"
                >
                  <option value="p50">p50</option>
                  <option value="p95">p95</option>
                  <option value="p99">p99</option>
                </select>
              </Field>
            ) : null}
            <div className="grid gap-4 sm:grid-cols-3">
              <Field label="Operator" htmlFor="alert-operator">
                <select
                  id="alert-operator"
                  value={operator}
                  onChange={(event) => setOperator(event.target.value)}
                  className="min-h-11 w-full rounded-md border border-graphite bg-carbon px-3 text-sm text-mist outline-none focus:border-acid-lime/70"
                >
                  <option value="gt">Greater than</option>
                  <option value="gte">At least</option>
                  <option value="lt">Less than</option>
                  <option value="lte">At most</option>
                </select>
              </Field>
              <Field
                label={
                  kind === CreateAdminAlertRuleRequestKind.latency
                    ? "Threshold (ms)"
                    : "Threshold"
                }
                htmlFor="alert-threshold"
              >
                <Input
                  id="alert-threshold"
                  type="number"
                  step="any"
                  value={threshold}
                  onChange={(event) => setThreshold(Number(event.target.value))}
                  required
                />
              </Field>
              <Field label="Window (seconds)" htmlFor="alert-window">
                <Input
                  id="alert-window"
                  type="number"
                  min={30}
                  max={86400}
                  value={windowSeconds}
                  onChange={(event) =>
                    setWindowSeconds(Number(event.target.value))
                  }
                  required
                />
              </Field>
            </div>
            <p className="text-[11px] leading-5 text-fog">
              Error-rate and health thresholds use a fraction from 0 to 1.
              Measurements are evaluated only when the selected telemetry signal
              has samples.
            </p>
          </div>
        ) : null}
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Severity" htmlFor="alert-severity">
            <select
              id="alert-severity"
              value={severity}
              onChange={(event) =>
                setSeverity(
                  event.target.value as CreateAlertRequest["severity"],
                )
              }
              className="min-h-11 w-full rounded-md border border-graphite bg-carbon px-3.5 text-sm text-mist outline-none focus:border-acid-lime/70 focus:ring-2 focus:ring-acid-lime/15"
            >
              <option value="info">Info</option>
              <option value="warning">Warning</option>
              <option value="critical">Critical</option>
            </select>
          </Field>
          <Field label="For (seconds)" htmlFor="alert-for">
            <Input
              id="alert-for"
              type="number"
              min={0}
              max={86400}
              value={forSeconds}
              onChange={(event) => setForSeconds(Number(event.target.value))}
            />
          </Field>
        </div>
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
          <Button
            type="submit"
            disabled={
              mutation.isPending ||
              (requiresMonitor && (!availableMonitors.length || !monitorId))
            }
          >
            {mutation.isPending ? "Creating…" : "Create rule"}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  );
}

function alertKindLabel(kind: CreateAlertRequest["kind"]) {
  switch (kind) {
    case CreateAdminAlertRuleRequestKind.metric_threshold:
      return "metric threshold";
    case CreateAdminAlertRuleRequestKind.error_rate:
      return "HTTP error rate";
    case CreateAdminAlertRuleRequestKind.latency:
      return "HTTP latency";
    case CreateAdminAlertRuleRequestKind.log_match:
      return "log match";
    case CreateAdminAlertRuleRequestKind.service_health:
      return "service health";
    case CreateAdminAlertRuleRequestKind.disk_pressure:
      return "disk pressure";
    case CreateAdminAlertRuleRequestKind.heartbeat_failure:
      return "heartbeat missing";
    case CreateAdminAlertRuleRequestKind.certificate_expiry:
      return "certificate expiry";
    default:
      return "monitor failure";
  }
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
      {hint ? <p className="text-[11px] leading-5 text-fog">{hint}</p> : null}
    </div>
  );
}
