"use client";

import { useMemo, useState, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import { AppWindow, FileUp, Pencil, Trash2 } from "lucide-react";
import { toast } from "sonner";
import {
  useCreateAppDeployment,
  useDeleteApp,
  useSelectAppDeployment,
  useUpdateApp,
} from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { useApp, useAppDeployments, useApps } from "@/api/queries";
import { createLogSource, LogViewer } from "@/components/log-viewer";
import { DataTable, type DataTableColumnDef } from "@/components/data-table";
import { formatBytes, formatDate } from "@/lib/format";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { AppEditorDialog } from "@/features/apps/app-editor-dialog";
import { AppDeploymentDialog } from "@/features/apps/app-deployment-dialog";
import { AppEnvironmentVariablesPanel } from "@/features/apps/app-environment-variables-panel";
import { updateAppPayload, type AppFormValues } from "@/features/apps/app-form";
import { BackLink } from "@/features/resources/detail-shared";
import type { AppDeployment } from "@/api/types";
import {
  AppHealth_status,
  AppRoute_status,
  AppRuntime_status,
} from "@/api/generated/schema";
import { pageControls } from "@/lib/pagination";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";

export function AppDetailView({
  organizationId,
  projectId,
  appId,
}: {
  organizationId: string;
  projectId: string;
  appId: string;
}) {
  const router = useRouter();
  const query = useApp(projectId, appId);
  const access = useApps(projectId, { limit: 1 });
  const deploymentNavigation = useCursorPagination("app_deployments_cursor");
  const deployments = useAppDeployments(projectId, appId, {
    cursor: deploymentNavigation.cursor,
    limit: 10,
  });
  const update = useUpdateApp(projectId, appId);
  const remove = useDeleteApp(projectId, appId);
  const createDeployment = useCreateAppDeployment(projectId, appId);
  const selectDeployment = useSelectAppDeployment(projectId, appId);
  const [editOpen, setEditOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deploymentOpen, setDeploymentOpen] = useState(false);
  const [inspectedDeploymentId, setInspectedDeploymentId] = useState<
    string | null
  >(null);
  const app = query.data?.app;
  const canManage = access.data?.can_manage === true;
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const deploymentItems = deployments.data?.deployments ?? [];
  const inspectedDeployment = deploymentItems.find(
    (deployment) => deployment.id === inspectedDeploymentId,
  );
  const buildLogSource = useMemo(
    () =>
      inspectedDeploymentId
        ? createLogSource({
            kind: "app-build",
            projectId,
            appId,
            deploymentId: inspectedDeploymentId,
          })
        : null,
    [appId, inspectedDeploymentId, projectId],
  );
  const runtimeLogSource = useMemo(
    () => createLogSource({ kind: "app-runtime", projectId, appId }),
    [appId, projectId],
  );

  const onUpdate = async (values: AppFormValues) => {
    await update.mutateAsync(updateAppPayload(values));
    toast.success("App configuration saved");
  };
  const onDelete = async () => {
    try {
      await remove.mutateAsync();
      toast.success("App deleted");
      router.push(`${base}/apps`);
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not delete App",
      );
    }
  };
  const onCreateDeployment = async (form: FormData) => {
    const result = await createDeployment.mutateAsync(form);
    const id = result?.deployment.id;
    if (id) setInspectedDeploymentId(id);
    toast.success("App build deployment queued");
  };
  const onSelectDeployment = async (deploymentId: string) => {
    try {
      await selectDeployment.mutateAsync(deploymentId);
      toast.success(
        "Desired image selected; runtime reconciliation has been queued.",
      );
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not select image",
      );
    }
  };

  if (query.isError) {
    return (
      <ErrorState
        title="Could not load App"
        error={query.error}
        retry={() => query.refetch()}
      />
    );
  }
  if (query.isPending) return <LoadingState rows={6} />;
  if (!app) {
    return (
      <ErrorState
        title="App not found"
        error={
          new Error("The App may have been removed or is outside this project.")
        }
      />
    );
  }

  return (
    <>
      <BackLink href={`${base}/apps`} label="Back to Apps" />
      <PageHeader
        eyebrow="Persistent App"
        title={app.name}
        description="Desired configuration, process runtime, application health and route eligibility."
        actions={
          canManage ? (
            <>
              <Button variant="outline" onClick={() => setEditOpen(true)}>
                <Pencil className="size-4" aria-hidden="true" /> Edit App
              </Button>
              <Button variant="destructive" onClick={() => setDeleteOpen(true)}>
                <Trash2 className="size-4" aria-hidden="true" /> Delete
              </Button>
            </>
          ) : access.data ? (
            <Badge variant="neutral">Read-only</Badge>
          ) : null
        }
      />
      <div className="mb-5 flex flex-wrap items-center gap-2">
        <Badge variant="neutral">
          {app.enabled ? "Enabled · desired" : "Disabled · desired"}
        </Badge>
        <span className="text-xs text-fog">
          Updated {formatDate(app.updated_at)}
        </span>
        <ResourceId id={app.id} label="App ID" />
      </div>

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
      ) : deploymentItems.length === 0 && !deployments.isPending ? (
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
          <Metric label="Runtime">
            <StatusBadge status={app.runtime_status} />
          </Metric>
          <Metric label="Health">
            <HealthBadge status={app.health_status} />
          </Metric>
          <Metric label="Public route">
            <RouteBadge
              status={app.route_status}
              healthStatus={app.health_status}
            />
          </Metric>
        </CardContent>
        <CardContent className="border-t border-white/[0.06] py-3">
          <Metric label="Platform hostname">
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
          </Metric>
        </CardContent>
      </Card>

      <div className="grid gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Desired configuration</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-4 sm:grid-cols-2">
            <Metric label="WorkloadSpec version">
              {app.workload.schema_version}
            </Metric>
            <Metric label="Spec digest">
              <span className="break-all font-mono text-[11px]">
                {app.workload_spec_sha256}
              </span>
            </Metric>
            <Metric label="Desired generation">{app.desired_generation}</Metric>
            <Metric label="Observed generation">
              {app.observed_generation}
            </Metric>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Resources and process</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-4 sm:grid-cols-2">
            <Metric label="Internal HTTP port">:{app.workload.port}</Metric>
            <Metric label="CPU">
              {app.workload.resources.cpu_millis} mCPU
            </Metric>
            <Metric label="Memory">
              {formatBytes(app.workload.resources.memory_bytes)}
            </Metric>
            <Metric label="PIDs limit">
              {app.workload.resources.pids_limit}
            </Metric>
            <Metric label="Working directory">
              <span className="font-mono text-xs">
                {app.workload.working_directory ?? "Image default"}
              </span>
            </Metric>
            <Metric label="Stop grace period">
              {app.workload.stop_grace_period_seconds} seconds
            </Metric>
            <Metric label="Restart policy">Always</Metric>
            <Metric label="Command arguments">
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
            </Metric>
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
            <Metric label="Protocol">
              {app.workload.health_check.protocol.toUpperCase()}
            </Metric>
            <Metric label="Path">
              {app.workload.health_check.path ?? "None for TCP"}
            </Metric>
            <Metric label="Interval">
              {app.workload.health_check.interval_seconds} seconds
            </Metric>
            <Metric label="Timeout">
              {app.workload.health_check.timeout_seconds} seconds
            </Metric>
            <Metric label="Initial delay">
              {app.workload.health_check.initial_delay_seconds} seconds
            </Metric>
            <Metric label="Failure threshold">
              {app.workload.health_check.failure_threshold}
            </Metric>
          </CardContent>
        </Card>
      </div>

      <Card className="mt-5">
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
          <div>
            <CardTitle>Build deployments</CardTitle>
            <p className="mt-1 text-xs leading-5 text-fog">
              Immutable OCI build artifacts and their captured WorkloadSpec
              snapshots. A ready image is not a running App.
            </p>
          </div>
          {canManage ? (
            <Button variant="outline" onClick={() => setDeploymentOpen(true)}>
              <FileUp className="size-4" aria-hidden="true" /> Create build
              deployment
            </Button>
          ) : null}
        </CardHeader>
        {deployments.isError ? (
          <CardContent>
            <ErrorState
              title="Could not load App deployments"
              error={deployments.error}
              retry={() => deployments.refetch()}
            />
          </CardContent>
        ) : (
          <DataTable
            data={deploymentItems}
            columns={deploymentColumns(
              app.desired_deployment_id,
              inspectedDeploymentId,
              setInspectedDeploymentId,
              canManage,
              selectDeployment.isPending,
              onSelectDeployment,
            )}
            loading={deployments.isPending}
            empty="No build deployments have been created yet."
            serverPagination={pageControls(
              deploymentNavigation,
              nextCursor(deployments.data),
              deployments.isFetching,
            )}
          />
        )}
      </Card>

      <AppEnvironmentVariablesPanel projectId={projectId} appId={appId} />

      <LogViewer
        key={`runtime:${app.id}`}
        title="Runtime logs"
        description="stdout/stderr captured from verified App containers and retained by the platform telemetry policy."
        source={runtimeLogSource}
        emptyMessage="No retained runtime log lines were returned for this App."
      />

      {inspectedDeployment ? (
        <div className="mt-5 space-y-4">
          <Card>
            <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
              <div>
                <CardTitle>
                  App build deployment v{inspectedDeployment.version}
                </CardTitle>
                <p className="mt-1 text-xs text-fog">
                  Created {formatDate(inspectedDeployment.created_at)} ·{" "}
                  {inspectedDeployment.source_name ?? "Source archive"}
                </p>
              </div>
              <StatusBadge status={inspectedDeployment.build_status} />
            </CardHeader>
            <CardContent className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
              <Metric label="Source SHA-256">
                <span className="break-all font-mono text-[11px]">
                  {inspectedDeployment.source_checksum_sha256}
                </span>
              </Metric>
              <Metric label="Dockerfile">
                {inspectedDeployment.dockerfile_path}
              </Metric>
              <Metric label="Context directory">
                {inspectedDeployment.context_directory}
              </Metric>
              <Metric label="Target">
                {inspectedDeployment.target ?? "Default stage"}
              </Metric>
              <Metric label="Platform">{inspectedDeployment.platform}</Metric>
              <Metric label="WorkloadSpec snapshot">
                {inspectedDeployment.workload_snapshot.schema_version} ·{" "}
                {inspectedDeployment.workload_spec_sha256.slice(0, 16)}…
              </Metric>
              <Metric label="Image digest">
                <span className="break-all font-mono text-[11px]">
                  {inspectedDeployment.image_digest ?? "Not produced"}
                </span>
              </Metric>
              <Metric label="OCI archive SHA-256">
                <span className="break-all font-mono text-[11px]">
                  {inspectedDeployment.image_archive_sha256 ?? "Not produced"}
                </span>
              </Metric>
              <Metric label="Image archive size">
                {inspectedDeployment.image_size_bytes
                  ? formatBytes(inspectedDeployment.image_size_bytes)
                  : "Not produced"}
              </Metric>
              <Metric label="Selection">
                {app.desired_deployment_id === inspectedDeployment.id
                  ? "Desired image selected"
                  : "Not selected"}
              </Metric>
              <Metric label="Build started">
                {inspectedDeployment.build_started_at
                  ? formatDate(inspectedDeployment.build_started_at)
                  : "Not started"}
              </Metric>
              <Metric label="Built">
                {inspectedDeployment.built_at
                  ? formatDate(inspectedDeployment.built_at)
                  : "Not built"}
              </Metric>
              {inspectedDeployment.error_message ? (
                <Metric label="Build error">
                  {inspectedDeployment.error_message}
                </Metric>
              ) : null}
            </CardContent>
          </Card>
          <LogViewer
            key={inspectedDeployment.id}
            title="Build logs"
            description="Bounded output from the trusted App build worker."
            source={buildLogSource}
            polling={
              inspectedDeployment.status === "queued" ||
              inspectedDeployment.status === "building"
            }
            emptyMessage="Build output will appear when this deployment starts."
          />
        </div>
      ) : null}

      <AppDeploymentDialog
        open={deploymentOpen}
        onOpenChange={setDeploymentOpen}
        pending={createDeployment.isPending}
        onSubmit={onCreateDeployment}
      />
      <AppEditorDialog
        open={editOpen}
        onOpenChange={setEditOpen}
        app={app}
        pending={update.isPending}
        onSubmit={onUpdate}
      />
      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete {app.name}?</DialogTitle>
            <DialogDescription>
              This removes the App configuration and releases its reserved
              hostname. Any managed runtime is queued for cleanup.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="ghost"
              disabled={remove.isPending}
              onClick={() => setDeleteOpen(false)}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={remove.isPending}
              onClick={() => void onDelete()}
            >
              {remove.isPending ? "Deleting…" : "Delete App"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
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

function Metric({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0 space-y-1">
      <p className="text-[10px] font-medium uppercase tracking-[0.12em] text-fog">
        {label}
      </p>
      <div className="text-sm text-mist">{children}</div>
    </div>
  );
}

function deploymentColumns(
  desiredDeploymentId: string | null,
  inspectedDeploymentId: string | null,
  setInspectedDeploymentId: (id: string | null) => void,
  canManage: boolean,
  selectionPending: boolean,
  onSelect: (id: string) => Promise<void>,
): DataTableColumnDef<AppDeployment>[] {
  return [
    {
      accessorKey: "version",
      header: "Version",
      cell: ({ row }) => `v${row.original.version}`,
    },
    {
      accessorKey: "source_name",
      header: "Source archive",
      cell: ({ row }) => row.original.source_name ?? "Source upload",
    },
    {
      accessorKey: "build_status",
      header: "Build",
      cell: ({ row }) => <StatusBadge status={row.original.build_status} />,
    },
    {
      accessorKey: "platform",
      header: "Platform",
      cell: ({ row }) => (
        <span className="font-mono text-xs">{row.original.platform}</span>
      ),
    },
    {
      id: "image",
      header: "Image artifact",
      cell: ({ row }) => (
        <div className="max-w-56 space-y-1">
          <span
            className="block truncate font-mono text-[11px]"
            title={row.original.image_digest ?? undefined}
          >
            {row.original.image_digest ?? "Not produced"}
          </span>
          <span className="block text-[11px] text-fog">
            {row.original.image_size_bytes
              ? formatBytes(row.original.image_size_bytes)
              : "Not reported"}
          </span>
        </div>
      ),
    },
    {
      id: "selection",
      header: "Desired image",
      cell: ({ row }) =>
        desiredDeploymentId === row.original.id ? (
          <Badge variant="success">Selected</Badge>
        ) : (
          <Badge variant="neutral">Not selected</Badge>
        ),
    },
    {
      accessorKey: "created_at",
      header: "Created",
      cell: ({ row }) => (
        <span className="whitespace-nowrap">
          {formatDate(row.original.created_at)}
        </span>
      ),
    },
    {
      id: "actions",
      header: "",
      cell: ({ row }) => (
        <div className="flex items-center justify-end gap-2">
          <Button
            size="sm"
            variant="ghost"
            aria-expanded={inspectedDeploymentId === row.original.id}
            onClick={() =>
              setInspectedDeploymentId(
                inspectedDeploymentId === row.original.id
                  ? null
                  : row.original.id,
              )
            }
          >
            {inspectedDeploymentId === row.original.id ? "Close" : "Inspect"}
          </Button>
          {canManage &&
          row.original.build_status === "succeeded" &&
          desiredDeploymentId !== row.original.id ? (
            <Button
              size="sm"
              variant="outline"
              disabled={selectionPending}
              onClick={() => void onSelect(row.original.id)}
            >
              Select as desired image
            </Button>
          ) : null}
        </div>
      ),
    },
  ];
}
