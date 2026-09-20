"use client";

import { useMemo, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { Activity, Copy, Plus, Trash2 } from "lucide-react";
import { useCreateAdminMonitor, useDeleteAdminMonitor } from "@/api/mutations";
import { useAdminMonitor, useAdminMonitors } from "@/api/queries";
import {
  CreateAdminMonitorRequestKind,
  CreateAdminMonitorRequestMethod,
  CreateAdminMonitorRequestRecord_type,
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
import { Textarea } from "@/components/ui/textarea";
import { formatDate, formatDuration } from "@/lib/format";
import { AdminShell } from "./admin-shell";
import { AdminTimeRange, useAdminTimeRange } from "./admin-time-range";

type MonitorKind = components["schemas"]["CreateAdminMonitorRequest"]["kind"];
type CreateMonitorRequest = components["schemas"]["CreateAdminMonitorRequest"];

const monitorKinds: Array<{ value: MonitorKind; label: string }> = [
  { value: CreateAdminMonitorRequestKind.http, label: "HTTP / HTTPS" },
  { value: CreateAdminMonitorRequestKind.tcp, label: "TCP" },
  { value: CreateAdminMonitorRequestKind.dns, label: "DNS" },
  { value: CreateAdminMonitorRequestKind.tls, label: "TLS certificate" },
  { value: CreateAdminMonitorRequestKind.heartbeat, label: "Heartbeat" },
];

const initialForm: CreateMonitorRequest = {
  name: "",
  kind: CreateAdminMonitorRequestKind.http,
  target: "",
  interval_seconds: 60,
  timeout_ms: 5000,
  enabled: true,
  method: CreateAdminMonitorRequestMethod.GET,
  expected_status: 200,
};

export function AdminMonitorsView() {
  const timeRange = useAdminTimeRange();
  const monitors = useAdminMonitors(
    { limit: 100 },
    { refetchInterval: timeRange.refreshInterval },
  );
  const [selectedId, setSelectedId] = useState<string>();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [heartbeatToken, setHeartbeatToken] = useState<string>();
  const [heartbeatEndpoint, setHeartbeatEndpoint] = useState<string>();
  const create = useCreateAdminMonitor();
  const remove = useDeleteAdminMonitor();
  const selected = useAdminMonitor(selectedId, {
    refetchInterval: timeRange.refreshInterval,
  });

  const sortedMonitors = useMemo(
    () => monitors.data?.items ?? [],
    [monitors.data?.items],
  );

  return (
    <AdminShell>
      <PageHeader
        eyebrow="Admin / Monitoring"
        title="Monitors"
        description="Persistent checks run by the trusted Stealth worker. Secrets stay encrypted and are never returned after creation."
        actions={
          <div className="flex flex-wrap items-center justify-end gap-3">
            <AdminTimeRange
              rangeKey={timeRange.rangeKey}
              refreshKey={timeRange.refreshKey}
              customRange={timeRange.customRange}
              onRangeChange={timeRange.setRange}
              onRefreshChange={timeRange.setRefresh}
              onCustomRangeChange={timeRange.setCustomRange}
            />
            <Dialog
              open={dialogOpen}
              onOpenChange={(open) => {
                setDialogOpen(open);
                if (!open) {
                  create.reset();
                  setHeartbeatToken(undefined);
                  setHeartbeatEndpoint(undefined);
                }
              }}
            >
              <DialogTrigger asChild>
                <Button size="sm">
                  <Plus className="size-3.5" aria-hidden="true" /> Add monitor
                </Button>
              </DialogTrigger>
              <CreateMonitorDialog
                mutation={create}
                heartbeatToken={heartbeatToken}
                heartbeatEndpoint={heartbeatEndpoint}
                onHeartbeatToken={(token, endpoint) => {
                  setHeartbeatToken(token);
                  setHeartbeatEndpoint(endpoint);
                }}
                onCreated={() => {
                  setDialogOpen(false);
                  setHeartbeatToken(undefined);
                  setHeartbeatEndpoint(undefined);
                }}
              />
            </Dialog>
          </div>
        }
      />

      {monitors.isPending ? <LoadingState rows={5} /> : null}
      {monitors.error ? (
        <ErrorState
          title="Could not load monitors"
          error={monitors.error}
          retry={() => monitors.refetch()}
        />
      ) : null}
      {monitors.data && !sortedMonitors.length ? (
        <EmptyMonitors onAdd={() => setDialogOpen(true)} />
      ) : null}
      {sortedMonitors.length ? (
        <div className="space-y-4">
          <div className="grid gap-3 sm:grid-cols-3">
            <MonitorSummary
              label="Total"
              value={sortedMonitors.length}
              tone="neutral"
            />
            <MonitorSummary
              label="Healthy"
              value={
                sortedMonitors.filter((item) => item.status === "healthy")
                  .length
              }
              tone="success"
            />
            <MonitorSummary
              label="Needs attention"
              value={
                sortedMonitors.filter((item) =>
                  ["failing", "degraded"].includes(item.status),
                ).length
              }
              tone="error"
            />
          </div>
          <div className="overflow-hidden rounded-xl border border-graphite bg-carbon">
            <div className="overflow-x-auto">
              <table className="w-full min-w-[880px] text-left text-sm">
                <thead className="border-b border-graphite bg-white/[0.02] text-xs uppercase tracking-[0.1em] text-fog">
                  <tr>
                    <th className="px-4 py-3 font-medium">Monitor</th>
                    <th className="px-4 py-3 font-medium">Type</th>
                    <th className="px-4 py-3 font-medium">Status</th>
                    <th className="px-4 py-3 font-medium">Last check</th>
                    <th className="px-4 py-3 font-medium">Next check</th>
                    <th
                      className="px-4 py-3 font-medium"
                      aria-label="Actions"
                    />
                  </tr>
                </thead>
                <tbody className="divide-y divide-graphite">
                  {sortedMonitors.map((monitor) => (
                    <tr
                      key={monitor.id}
                      className="cursor-pointer align-top transition-colors duration-150 hover:bg-white/[0.025]"
                      onClick={() => setSelectedId(monitor.id)}
                    >
                      <td className="px-4 py-4">
                        <p className="text-mist">{monitor.name}</p>
                        <p className="mt-1 max-w-[360px] truncate font-mono text-[11px] text-fog">
                          {monitor.target}
                        </p>
                      </td>
                      <td className="px-4 py-4">
                        <Badge variant="neutral">{monitor.kind}</Badge>
                      </td>
                      <td className="px-4 py-4">
                        <StatusBadge
                          status={monitor.enabled ? monitor.status : "paused"}
                        />
                      </td>
                      <td className="px-4 py-4 text-xs text-fog">
                        <p>{formatDate(monitor.last_checked_at)}</p>
                        {monitor.last_latency_ms !== null &&
                        monitor.last_latency_ms !== undefined ? (
                          <p className="mt-1 font-mono text-[11px] text-mist">
                            {formatDuration(monitor.last_latency_ms)}
                          </p>
                        ) : null}
                      </td>
                      <td className="px-4 py-4 text-xs text-fog">
                        {monitor.enabled
                          ? formatDate(monitor.next_check_at)
                          : "Paused"}
                      </td>
                      <td className="px-4 py-4 text-right">
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          aria-label={`Delete ${monitor.name}`}
                          onClick={(event) => {
                            event.stopPropagation();
                            if (
                              window.confirm(
                                `Delete the monitor “${monitor.name}” and its check history?`,
                              )
                            ) {
                              remove.mutate(monitor.id);
                            }
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
          </div>
          {remove.error ? (
            <p className="text-sm text-coral-red" role="alert">
              {errorMessage(remove.error)}
            </p>
          ) : null}
        </div>
      ) : null}

      <Dialog
        open={Boolean(selectedId)}
        onOpenChange={(open) => {
          if (!open) setSelectedId(undefined);
        }}
      >
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>
              {selected.data?.monitor.name ?? "Monitor details"}
            </DialogTitle>
            <DialogDescription>
              Recent worker checks and the safe monitor projection. Secret
              configuration is intentionally unavailable.
            </DialogDescription>
          </DialogHeader>
          {selected.isPending ? <LoadingState rows={3} /> : null}
          {selected.error ? (
            <ErrorState
              error={selected.error}
              retry={() => selected.refetch()}
            />
          ) : null}
          {selected.data ? <MonitorDetails data={selected.data} /> : null}
          <DialogFooter>
            <DialogClose asChild>
              <Button variant="secondary">Close</Button>
            </DialogClose>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </AdminShell>
  );
}

function CreateMonitorDialog({
  mutation,
  heartbeatToken,
  heartbeatEndpoint,
  onHeartbeatToken,
  onCreated,
}: {
  mutation: ReturnType<typeof useCreateAdminMonitor>;
  heartbeatToken: string | undefined;
  heartbeatEndpoint: string | undefined;
  onHeartbeatToken: (token: string, endpoint: string) => void;
  onCreated: () => void;
}) {
  const [form, setForm] = useState<CreateMonitorRequest>(initialForm);
  const [headersJSON, setHeadersJSON] = useState("");
  const [parseError, setParseError] = useState<string>();
  const kind = form.kind;
  const set = <K extends keyof CreateMonitorRequest>(
    key: K,
    value: CreateMonitorRequest[K],
  ) => setForm((current) => ({ ...current, [key]: value }));

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setParseError(undefined);
    let headers: Record<string, string> | undefined;
    if (headersJSON.trim()) {
      try {
        const value: unknown = JSON.parse(headersJSON);
        if (!value || typeof value !== "object" || Array.isArray(value)) {
          throw new Error("Headers must be a JSON object.");
        }
        headers = Object.fromEntries(
          Object.entries(value).map(([key, value]) => {
            if (typeof value !== "string")
              throw new Error("Header values must be strings.");
            return [key, value];
          }),
        );
      } catch (error) {
        mutation.reset();
        // The form stays open so a secret header is not lost to a validation
        // error. Only the local parse message is shown.
        setParseError(
          error instanceof Error ? error.message : "Headers are invalid.",
        );
        return;
      }
    }
    const body: CreateMonitorRequest = {
      ...form,
      headers,
      method: kind === "http" ? form.method : undefined,
      expected_status: form.expected_status ?? 200,
      body: kind === "http" ? form.body : undefined,
      body_contains: kind === "http" ? form.body_contains : undefined,
      host: kind === "tcp" || kind === "tls" ? form.host : undefined,
      port: kind === "tcp" || kind === "tls" ? form.port : undefined,
      record_type: kind === "dns" ? form.record_type : undefined,
      expected_values: kind === "dns" ? form.expected_values : undefined,
      grace_seconds: kind === "heartbeat" ? form.grace_seconds : undefined,
    };
    mutation.mutate(body, {
      onSuccess: (response) => {
        if (response?.heartbeat_token) {
          onHeartbeatToken(
            response.heartbeat_token,
            response.heartbeat_endpoint ?? "",
          );
        } else {
          onCreated();
        }
      },
    });
  }

  return (
    <DialogContent className="max-w-2xl">
      <DialogHeader>
        <DialogTitle>Add monitor</DialogTitle>
        <DialogDescription>
          The worker executes checks on a bounded schedule. Network targets are
          restricted to public addresses to reduce SSRF risk.
        </DialogDescription>
      </DialogHeader>
      {heartbeatToken ? (
        <HeartbeatTokenNotice
          token={heartbeatToken}
          endpoint={heartbeatEndpoint}
        />
      ) : (
        <form className="space-y-4" onSubmit={submit}>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Name" htmlFor="monitor-name">
              <Input
                id="monitor-name"
                required
                value={form.name ?? ""}
                onChange={(event) => set("name", event.target.value)}
                placeholder="Public API"
              />
            </Field>
            <Field label="Type" htmlFor="monitor-kind">
              <select
                id="monitor-kind"
                value={form.kind}
                onChange={(event) => {
                  const nextKind = event.target.value as MonitorKind;
                  setForm((current) => ({ ...current, kind: nextKind }));
                }}
                className="min-h-11 w-full rounded-md border border-graphite bg-carbon px-3.5 text-sm text-mist outline-none focus:border-acid-lime/70 focus:ring-2 focus:ring-acid-lime/15"
              >
                {monitorKinds.map((item) => (
                  <option key={item.value} value={item.value}>
                    {item.label}
                  </option>
                ))}
              </select>
            </Field>
          </div>
          <Field
            label="Target"
            htmlFor="monitor-target"
            hint={
              kind === "heartbeat"
                ? "A label for the job; the generated endpoint is shown after creation."
                : undefined
            }
          >
            <Input
              id="monitor-target"
              required
              value={form.target ?? ""}
              onChange={(event) => set("target", event.target.value)}
              placeholder={targetPlaceholder(kind)}
            />
          </Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Interval (seconds)" htmlFor="monitor-interval">
              <Input
                id="monitor-interval"
                type="number"
                min={5}
                max={86400}
                value={form.interval_seconds ?? 60}
                onChange={(event) =>
                  set("interval_seconds", Number(event.target.value))
                }
              />
            </Field>
            <Field label="Timeout (milliseconds)" htmlFor="monitor-timeout">
              <Input
                id="monitor-timeout"
                type="number"
                min={100}
                max={120000}
                value={form.timeout_ms ?? 5000}
                onChange={(event) =>
                  set("timeout_ms", Number(event.target.value))
                }
              />
            </Field>
          </div>
          {kind === "http" ? (
            <div className="space-y-4 rounded-md border border-graphite bg-void/40 p-4">
              <div className="grid gap-4 sm:grid-cols-2">
                <Field label="Method" htmlFor="monitor-method">
                  <select
                    id="monitor-method"
                    value={form.method ?? "GET"}
                    onChange={(event) =>
                      set(
                        "method",
                        event.target.value as CreateAdminMonitorRequestMethod,
                      )
                    }
                    className="min-h-11 w-full rounded-md border border-graphite bg-carbon px-3.5 text-sm text-mist outline-none focus:border-acid-lime/70 focus:ring-2 focus:ring-acid-lime/15"
                  >
                    {["GET", "HEAD", "POST", "PUT", "PATCH", "OPTIONS"].map(
                      (method) => (
                        <option key={method} value={method}>
                          {method}
                        </option>
                      ),
                    )}
                  </select>
                </Field>
                <Field label="Expected status" htmlFor="monitor-status">
                  <Input
                    id="monitor-status"
                    type="number"
                    min={100}
                    max={599}
                    value={form.expected_status ?? 200}
                    onChange={(event) =>
                      set("expected_status", Number(event.target.value))
                    }
                  />
                </Field>
              </div>
              <Field
                label="Secret headers (JSON)"
                htmlFor="monitor-headers"
                hint="Write-only. Values are encrypted before persistence; they are not shown again."
              >
                <Textarea
                  id="monitor-headers"
                  value={headersJSON}
                  onChange={(event) => setHeadersJSON(event.target.value)}
                  placeholder={'{"Authorization":"Bearer …"}'}
                  className="min-h-20 font-mono text-xs"
                  autoComplete="off"
                />
              </Field>
              <Field
                label="Request body"
                htmlFor="monitor-body"
                hint="Write-only; max 1 MiB."
              >
                <Textarea
                  id="monitor-body"
                  value={form.body ?? ""}
                  onChange={(event) => set("body", event.target.value)}
                  className="min-h-20 font-mono text-xs"
                  autoComplete="off"
                />
              </Field>
              <Field
                label="Body contains"
                htmlFor="monitor-body-contains"
                hint="Write-only assertion; exact substring match."
              >
                <Input
                  id="monitor-body-contains"
                  value={form.body_contains ?? ""}
                  onChange={(event) => set("body_contains", event.target.value)}
                  autoComplete="off"
                />
              </Field>
            </div>
          ) : null}
          {kind === "tcp" || kind === "tls" ? (
            <div className="grid gap-4 rounded-md border border-graphite bg-void/40 p-4 sm:grid-cols-[1fr_160px]">
              <Field
                label="Host (optional)"
                htmlFor="monitor-host"
                hint="If omitted, use host:port in Target."
              >
                <Input
                  id="monitor-host"
                  value={form.host ?? ""}
                  onChange={(event) => set("host", event.target.value)}
                />
              </Field>
              <Field label="Port (optional)" htmlFor="monitor-port">
                <Input
                  id="monitor-port"
                  type="number"
                  min={1}
                  max={65535}
                  value={form.port ?? ""}
                  onChange={(event) =>
                    set("port", Number(event.target.value) || undefined)
                  }
                />
              </Field>
            </div>
          ) : null}
          {kind === "dns" ? (
            <div className="grid gap-4 rounded-md border border-graphite bg-void/40 p-4 sm:grid-cols-2">
              <Field label="Record type" htmlFor="monitor-record-type">
                <select
                  id="monitor-record-type"
                  value={
                    form.record_type ?? CreateAdminMonitorRequestRecord_type.A
                  }
                  onChange={(event) =>
                    set(
                      "record_type",
                      event.target
                        .value as CreateAdminMonitorRequestRecord_type,
                    )
                  }
                  className="min-h-11 w-full rounded-md border border-graphite bg-carbon px-3.5 text-sm text-mist outline-none focus:border-acid-lime/70 focus:ring-2 focus:ring-acid-lime/15"
                >
                  {["A", "AAAA", "CNAME", "TXT"].map((recordType) => (
                    <option key={recordType} value={recordType}>
                      {recordType}
                    </option>
                  ))}
                </select>
              </Field>
              <Field
                label="Expected values"
                htmlFor="monitor-expected-values"
                hint="Comma-separated; leave blank to assert lookup success only."
              >
                <Input
                  id="monitor-expected-values"
                  value={(form.expected_values ?? []).join(", ")}
                  onChange={(event) =>
                    set(
                      "expected_values",
                      event.target.value
                        .split(",")
                        .map((value) => value.trim())
                        .filter(Boolean),
                    )
                  }
                />
              </Field>
            </div>
          ) : null}
          {kind === "heartbeat" ? (
            <Field
              label="Grace period (seconds)"
              htmlFor="monitor-grace"
              hint="The token is generated after creation and shown once."
            >
              <Input
                id="monitor-grace"
                type="number"
                min={0}
                max={604800}
                value={form.grace_seconds ?? 0}
                onChange={(event) =>
                  set("grace_seconds", Number(event.target.value))
                }
              />
            </Field>
          ) : null}
          {parseError ? (
            <p className="text-sm text-coral-red" role="alert">
              {parseError}
            </p>
          ) : null}
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
              {mutation.isPending ? "Creating…" : "Create monitor"}
            </Button>
          </DialogFooter>
        </form>
      )}
    </DialogContent>
  );
}

function HeartbeatTokenNotice({
  token,
  endpoint,
}: {
  token: string;
  endpoint: string | undefined;
}) {
  return (
    <div className="space-y-4 rounded-md border border-acid-lime/25 bg-acid-lime/[0.06] p-4">
      <div>
        <p className="text-sm text-paper">Heartbeat monitor created</p>
        <p className="mt-1 text-sm leading-6 text-fog">
          Copy this token now. It is shown once and cannot be recovered from
          Stealth.
        </p>
      </div>
      <div className="flex items-center gap-2 rounded-md border border-graphite bg-carbon p-3">
        <code className="min-w-0 flex-1 break-all font-mono text-xs text-mist">
          {token}
        </code>
        <Button
          type="button"
          variant="secondary"
          size="icon"
          aria-label="Copy heartbeat token"
          onClick={() => void navigator.clipboard?.writeText(token)}
        >
          <Copy className="size-4" aria-hidden="true" />
        </Button>
      </div>
      <p className="break-all font-mono text-xs leading-5 text-fog">
        POST {endpoint || "the generated heartbeat endpoint"} with
        X-Stealth-Heartbeat: &lt;token&gt;.
      </p>
    </div>
  );
}

function MonitorDetails({
  data,
}: {
  data: components["schemas"]["AdminMonitorResponse"];
}) {
  const monitor = data.monitor;
  return (
    <div className="space-y-5">
      <div className="grid gap-3 sm:grid-cols-3">
        <Detail label="Status">
          <StatusBadge status={monitor.enabled ? monitor.status : "paused"} />
        </Detail>
        <Detail label="Target">
          <span className="break-all font-mono text-xs text-mist">
            {monitor.target}
          </span>
        </Detail>
        <Detail label="Schedule">
          <span className="font-mono text-xs text-mist">
            every {monitor.interval_seconds}s · {monitor.timeout_ms}ms timeout
          </span>
        </Detail>
      </div>
      {monitor.last_error ? (
        <p className="rounded-md border border-coral-red/20 bg-coral-red/[0.04] p-3 text-sm text-coral-red">
          {monitor.last_error}
        </p>
      ) : null}
      <div className="overflow-hidden rounded-md border border-graphite">
        <table className="w-full text-left text-xs">
          <thead className="border-b border-graphite bg-white/[0.02] text-fog">
            <tr>
              <th className="px-3 py-2 font-medium">Checked</th>
              <th className="px-3 py-2 font-medium">Result</th>
              <th className="px-3 py-2 font-medium">Latency</th>
              <th className="px-3 py-2 font-medium">Details</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-graphite">
            {(data.checks ?? []).map((check) => (
              <tr key={check.id}>
                <td className="px-3 py-2 text-fog">
                  {formatDate(check.checked_at)}
                </td>
                <td className="px-3 py-2">
                  <StatusBadge status={check.success ? "healthy" : "failing"} />
                </td>
                <td className="px-3 py-2 font-mono text-mist">
                  {formatDuration(check.latency_ms)}
                </td>
                <td className="max-w-[240px] break-words px-3 py-2 text-fog">
                  {check.error ?? JSON.stringify(check.details)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {!data.checks?.length ? (
          <p className="p-4 text-sm text-fog">
            No worker checks have completed yet.
          </p>
        ) : null}
      </div>
    </div>
  );
}

function Detail({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0 rounded-md border border-graphite bg-void/40 p-3">
      <p className="text-[11px] uppercase tracking-[0.1em] text-fog">{label}</p>
      <div className="mt-2">{children}</div>
    </div>
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
      {hint ? <p className="text-[11px] leading-5 text-fog">{hint}</p> : null}
    </div>
  );
}

function MonitorSummary({
  label,
  value,
  tone,
}: {
  label: string;
  value: number;
  tone: "neutral" | "success" | "error";
}) {
  const color =
    tone === "success"
      ? "text-pulse-green"
      : tone === "error"
        ? "text-coral-red"
        : "text-paper";
  return (
    <div className="rounded-xl border border-graphite bg-carbon p-4">
      <p className="text-xs uppercase tracking-[0.1em] text-fog">{label}</p>
      <p className={`mt-2 font-mono text-2xl tabular-nums ${color}`}>{value}</p>
    </div>
  );
}

function EmptyMonitors({ onAdd }: { onAdd: () => void }) {
  return (
    <div className="rounded-xl border border-dashed border-graphite bg-carbon/50 p-10 text-center">
      <Activity className="mx-auto size-5 text-fog" aria-hidden="true" />
      <h2 className="mt-4 text-sm text-paper">No monitors configured</h2>
      <p className="mx-auto mt-2 max-w-md text-sm leading-6 text-fog">
        Create an HTTP, TCP, DNS, TLS, or heartbeat check. Results will appear
        here after the worker completes its first run.
      </p>
      <Button className="mt-5" size="sm" onClick={onAdd}>
        <Plus className="size-3.5" aria-hidden="true" /> Add monitor
      </Button>
    </div>
  );
}

function targetPlaceholder(kind: MonitorKind) {
  switch (kind) {
    case "http":
      return "https://status.example.com/health";
    case "tcp":
    case "tls":
      return "db.example.com:5432";
    case "dns":
      return "example.com";
    case "heartbeat":
      return "nightly-backup";
    default:
      return "Target";
  }
}
