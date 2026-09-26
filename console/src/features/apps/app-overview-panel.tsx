import { AppWindow } from "lucide-react";
import { formatBytes } from "@/lib/format";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import type { StealthApp } from "@/api/types";
import {
  AppHealth_status,
  AppRoute_status,
  AppRuntime_status,
} from "@/api/generated/schema";
import { AppDetailMetric } from "@/features/apps/app-detail-metric";

export function AppOverviewPanel({
  app,
  deploymentCount,
  deploymentsPending,
}: {
  app: StealthApp;
  deploymentCount: number;
  deploymentsPending: boolean;
}) {
  return (
    <>
      {app.runtime_error ? (
        <div
          role="alert"
          className="mb-5 rounded-md border border-rose-300/25 bg-rose-300/[0.06] px-4 py-3 text-sm text-rose-100"
        >
          <p className="font-medium">Runtime reconciliation needs attention</p>
          <p className="mt-1 text-xs leading-5 text-rose-100/75">
            {app.runtime_error}
          </p>
        </div>
      ) : null}

      {app.desired_deployment_id ? (
        <Card className="mb-5 border-cyan-300/20 bg-cyan-300/[0.03]">
          <CardContent className="flex items-start gap-3 p-4">
            <AppWindow
              className="mt-0.5 size-4 shrink-0 text-cyan-200"
              aria-hidden="true"
            />
            <div>
              <p className="text-sm font-medium text-cyan-100">
                Desired image selected
              </p>
              <p className="mt-1 text-xs leading-5 text-cyan-100/70">
                This immutable image is the desired release.{" "}
                {runtimeSummary(
                  app.runtime_status,
                  app.desired_generation,
                  app.observed_generation,
                )}
              </p>
            </div>
          </CardContent>
        </Card>
      ) : deploymentCount === 0 && !deploymentsPending ? (
        <Card className="mb-5 border-cyan-300/20 bg-cyan-300/[0.03]">
          <CardContent className="flex items-start gap-3 p-4">
            <AppWindow
              className="mt-0.5 size-4 shrink-0 text-cyan-200"
              aria-hidden="true"
            />
            <div>
              <p className="text-sm font-medium text-cyan-100">
                No deployment has been created for this App yet.
              </p>
              <p className="mt-1 text-xs leading-5 text-cyan-100/70">
                Create a build deployment to produce an immutable OCI image.
                Building an image does not start this App or create a public
                route.
              </p>
            </div>
          </CardContent>
        </Card>
      ) : null}

      <Card className="mb-5">
        <CardHeader>
          <CardTitle>Availability</CardTitle>
          <p className="mt-1 text-xs leading-5 text-fog">
            Running confirms the expected container process. The public route
            waits for the current generation to pass its configured health
            check.
          </p>
        </CardHeader>
        <CardContent className="grid gap-4 sm:grid-cols-3">
          <AppDetailMetric label="Runtime">
            <StatusBadge status={app.runtime_status} />
          </AppDetailMetric>
          <AppDetailMetric label="Health">
            <HealthBadge status={app.health_status} />
          </AppDetailMetric>
          <AppDetailMetric label="Public route">
            <RouteBadge
              status={app.route_status}
              healthStatus={app.health_status}
            />
          </AppDetailMetric>
        </CardContent>
        <CardContent className="border-t border-white/[0.06] py-3">
          <AppDetailMetric label="Platform hostname">
            {app.platform_hostname ? (
              app.route_status === "active" ? (
                <a
                  className="inline-flex min-h-11 max-w-full items-center break-all font-mono text-xs text-cyan-200 underline decoration-cyan-200/30 underline-offset-4 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300"
                  href={`https://${app.platform_hostname}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  aria-label={`${app.platform_hostname} (opens in a new tab)`}
                >
                  {app.platform_hostname}
                </a>
              ) : (
                <span className="break-all font-mono text-xs">
                  {app.platform_hostname}
                </span>
              )
            ) : (
              <span className="text-fog">Not configured</span>
            )}
          </AppDetailMetric>
        </CardContent>
      </Card>

      <div className="grid gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Desired configuration</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-4 sm:grid-cols-2">
            <AppDetailMetric label="WorkloadSpec version">
              {app.workload.schema_version}
            </AppDetailMetric>
            <AppDetailMetric label="Spec digest">
              <span className="break-all font-mono text-[11px]">
                {app.workload_spec_sha256}
              </span>
            </AppDetailMetric>
            <AppDetailMetric label="Desired generation">
              {app.desired_generation}
            </AppDetailMetric>
            <AppDetailMetric label="Observed generation">
              {app.observed_generation}
            </AppDetailMetric>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Resources and process</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-4 sm:grid-cols-2">
            <AppDetailMetric label="Internal HTTP port">
              :{app.workload.port}
            </AppDetailMetric>
            <AppDetailMetric label="CPU">
              {app.workload.resources.cpu_millis} mCPU
            </AppDetailMetric>
            <AppDetailMetric label="Memory">
              {formatBytes(app.workload.resources.memory_bytes)}
            </AppDetailMetric>
            <AppDetailMetric label="PIDs limit">
              {app.workload.resources.pids_limit}
            </AppDetailMetric>
            <AppDetailMetric label="Working directory">
              <span className="font-mono text-xs">
                {app.workload.working_directory ?? "Image default"}
              </span>
            </AppDetailMetric>
            <AppDetailMetric label="Stop grace period">
              {app.workload.stop_grace_period_seconds} seconds
            </AppDetailMetric>
            <AppDetailMetric label="Restart policy">Always</AppDetailMetric>
            <AppDetailMetric label="Command arguments">
              {app.workload.command.length ? (
                <ol className="space-y-1 font-mono text-xs">
                  {app.workload.command.map((argument, index) => (
                    <li key={`${index}-${argument}`} className="break-all">
                      <span className="mr-2 text-ash">{index + 1}.</span>
                      {argument}
                    </li>
                  ))}
                </ol>
              ) : (
                <span className="text-xs text-fog">
                  Empty; future image defaults apply.
                </span>
              )}
            </AppDetailMetric>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Health check</CardTitle>
            <p className="mt-1 text-xs leading-5 text-fog">
              The worker probes this container on its internal port. HTTP checks
              pass on 2xx responses; redirects are not followed.
            </p>
          </CardHeader>
          <CardContent className="grid gap-4 sm:grid-cols-2">
            <AppDetailMetric label="Protocol">
              {app.workload.health_check.protocol.toUpperCase()}
            </AppDetailMetric>
            <AppDetailMetric label="Path">
              {app.workload.health_check.path ?? "None for TCP"}
            </AppDetailMetric>
            <AppDetailMetric label="Interval">
              {app.workload.health_check.interval_seconds} seconds
            </AppDetailMetric>
            <AppDetailMetric label="Timeout">
              {app.workload.health_check.timeout_seconds} seconds
            </AppDetailMetric>
            <AppDetailMetric label="Initial delay">
              {app.workload.health_check.initial_delay_seconds} seconds
            </AppDetailMetric>
            <AppDetailMetric label="Failure threshold">
              {app.workload.health_check.failure_threshold}
            </AppDetailMetric>
          </CardContent>
        </Card>
      </div>
    </>
  );
}

function runtimeSummary(
  status: AppRuntime_status,
  desired: number,
  observed: number,
) {
  switch (status) {
    case AppRuntime_status.pending:
      return `The runtime is reconciling generation ${desired}; generation ${observed} remains the last successfully applied state.`;
    case AppRuntime_status.running:
      return `The expected container is running and matches generation ${observed}. Application health is shown separately.`;
    case AppRuntime_status.degraded:
      return `The runtime is not currently matching the desired state. Generation ${observed} remains the last successfully applied state while the worker retries.`;
    case AppRuntime_status.stopped:
      return "The App is disabled and its managed container is absent.";
    case AppRuntime_status.failed:
      return `The desired runtime could not be applied. Generation ${observed} remains the last successfully applied state; the worker will retry.`;
    case AppRuntime_status.not_deployed:
      return "No deployment is selected, so no App container should be running.";
    default:
      return `Desired generation ${desired}; last observed generation ${observed}.`;
  }
}

function HealthBadge({ status }: { status: AppHealth_status }) {
  const state =
    status === AppHealth_status.healthy
      ? "success"
      : status === AppHealth_status.unhealthy
        ? "error"
        : "building";
  const label =
    status === AppHealth_status.healthy
      ? "Healthy"
      : status === AppHealth_status.unhealthy
        ? "Unhealthy"
        : "Starting";
  return (
    <Badge variant={state}>
      <span
        className={`size-1.5 rounded-full bg-current ${status === AppHealth_status.pending ? "animate-pulse motion-reduce:animate-none" : ""}`}
        aria-hidden="true"
      />
      {label}
    </Badge>
  );
}

function RouteBadge({
  status,
  healthStatus,
}: {
  status: AppRoute_status;
  healthStatus: AppHealth_status;
}) {
  const view = {
    [AppRoute_status.active]: { label: "Active", variant: "success" as const },
    [AppRoute_status.waiting_for_runtime]: {
      label: "Waiting for runtime",
      variant: "building" as const,
    },
    [AppRoute_status.waiting_for_health]: {
      label: "Waiting for health",
      variant: "warning" as const,
    },
    [AppRoute_status.not_available]: {
      label: "Not published",
      variant: "neutral" as const,
    },
  }[status];
  if (
    status === AppRoute_status.waiting_for_health &&
    healthStatus === AppHealth_status.unhealthy
  ) {
    return (
      <Badge variant="neutral">
        <span className="size-1.5 rounded-full bg-current" aria-hidden="true" />
        Not published
      </Badge>
    );
  }
  return (
    <Badge variant={view.variant}>
      <span className="size-1.5 rounded-full bg-current" aria-hidden="true" />
      {view.label}
    </Badge>
  );
}
