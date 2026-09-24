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
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { AppEditorDialog } from "@/features/apps/app-editor-dialog";
import { AppDeploymentDialog } from "@/features/apps/app-deployment-dialog";
import { updateAppPayload, type AppFormValues } from "@/features/apps/app-form";
import { BackLink } from "@/features/resources/detail-shared";
import type { AppDeployment } from "@/api/types";
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
  const [inspectedDeploymentId, setInspectedDeploymentId] = useState<string | null>(null);
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
      toast.error(error instanceof Error ? error.message : "Could not delete App");
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
      toast.success("Selected as desired image. Runtime remains not deployed.");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Could not select image");
    }
  };

  if (query.isError) {
    return <ErrorState title="Could not load App" error={query.error} retry={() => query.refetch()} />;
  }
  if (query.isPending) return <LoadingState rows={6} />;
  if (!app) {
    return (
      <ErrorState
        title="App not found"
        error={new Error("The App may have been removed or is outside this project.")}
      />
    );
  }

  return (
    <>
      <BackLink href={`${base}/apps`} label="Back to Apps" />
      <PageHeader
        eyebrow="Persistent App"
        title={app.name}
        description="Durable desired configuration for a future long-running project workload."
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
        <StatusBadge status={app.runtime_status} />
        <Badge variant="neutral">{app.enabled ? "Enabled · desired" : "Disabled · desired"}</Badge>
        <span className="text-xs text-fog">Updated {formatDate(app.updated_at)}</span>
        <ResourceId id={app.id} label="App ID" />
      </div>

      {app.desired_deployment_id ? (
        <Card className="mb-5 border-cyan-300/20 bg-cyan-300/[0.03]">
          <CardContent className="flex items-start gap-3 p-4">
            <AppWindow className="mt-0.5 size-4 shrink-0 text-cyan-200" aria-hidden="true" />
            <div>
              <p className="text-sm font-medium text-cyan-100">Desired image selected</p>
              <p className="mt-1 text-xs leading-5 text-cyan-100/70">
                This selection records which immutable image a future runtime should use. No container has been created; runtime status remains Not deployed.
              </p>
            </div>
          </CardContent>
        </Card>
      ) : deploymentItems.length === 0 && !deployments.isPending ? (
        <Card className="mb-5 border-cyan-300/20 bg-cyan-300/[0.03]">
          <CardContent className="flex items-start gap-3 p-4">
            <AppWindow className="mt-0.5 size-4 shrink-0 text-cyan-200" aria-hidden="true" />
            <div>
              <p className="text-sm font-medium text-cyan-100">No deployment has been created for this App yet.</p>
              <p className="mt-1 text-xs leading-5 text-cyan-100/70">
                Create a build deployment to produce an immutable OCI image. Building an image does not start this App or create a public route.
              </p>
            </div>
          </CardContent>
        </Card>
      ) : null}

      <div className="grid gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader><CardTitle>Runtime identity</CardTitle></CardHeader>
          <CardContent className="grid gap-4 sm:grid-cols-2">
            <Metric label="Reserved platform hostname">
              <span className="break-all font-mono text-xs">{app.platform_hostname ?? "Not configured"}</span>
            </Metric>
            <Metric label="WorkloadSpec version">{app.workload.schema_version}</Metric>
            <Metric label="Spec digest">
              <span className="break-all font-mono text-[11px]">{app.workload_spec_sha256}</span>
            </Metric>
            <Metric label="Desired generation">{app.desired_generation}</Metric>
            <Metric label="Observed generation">{app.observed_generation}</Metric>
            <Metric label="Runtime status"><StatusBadge status={app.runtime_status} /></Metric>
          </CardContent>
        </Card>

        <Card>
          <CardHeader><CardTitle>Resources and process</CardTitle></CardHeader>
          <CardContent className="grid gap-4 sm:grid-cols-2">
            <Metric label="Internal HTTP port">:{app.workload.port}</Metric>
            <Metric label="CPU">{app.workload.resources.cpu_millis} mCPU</Metric>
            <Metric label="Memory">{formatBytes(app.workload.resources.memory_bytes)}</Metric>
            <Metric label="PIDs limit">{app.workload.resources.pids_limit}</Metric>
            <Metric label="Working directory">
              <span className="font-mono text-xs">{app.workload.working_directory ?? "Image default"}</span>
            </Metric>
            <Metric label="Stop grace period">{app.workload.stop_grace_period_seconds} seconds</Metric>
            <Metric label="Restart policy">Always</Metric>
            <Metric label="Command arguments">
              {app.workload.command.length ? (
                <ol className="space-y-1 font-mono text-xs">
                  {app.workload.command.map((argument, index) => (
                    <li key={`${index}-${argument}`} className="break-all">
                      <span className="mr-2 text-ash">{index + 1}.</span>{argument}
                    </li>
                  ))}
                </ol>
              ) : (
                <span className="text-xs text-fog">Empty; future image defaults apply.</span>
              )}
            </Metric>
          </CardContent>
        </Card>

        <Card>
          <CardHeader><CardTitle>Health check</CardTitle></CardHeader>
          <CardContent className="grid gap-4 sm:grid-cols-2">
            <Metric label="Protocol">{app.workload.health_check.protocol.toUpperCase()}</Metric>
            <Metric label="Path">{app.workload.health_check.path ?? "None for TCP"}</Metric>
            <Metric label="Interval">{app.workload.health_check.interval_seconds} seconds</Metric>
            <Metric label="Timeout">{app.workload.health_check.timeout_seconds} seconds</Metric>
            <Metric label="Initial delay">{app.workload.health_check.initial_delay_seconds} seconds</Metric>
            <Metric label="Failure threshold">{app.workload.health_check.failure_threshold}</Metric>
          </CardContent>
        </Card>
      </div>

      <Card className="mt-5">
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
          <div>
            <CardTitle>Build deployments</CardTitle>
            <p className="mt-1 text-xs leading-5 text-fog">
              Immutable OCI build artifacts and their captured WorkloadSpec snapshots. A ready image is not a running App.
            </p>
          </div>
          {canManage ? (
            <Button variant="outline" onClick={() => setDeploymentOpen(true)}>
              <FileUp className="size-4" aria-hidden="true" /> Create build deployment
            </Button>
          ) : null}
        </CardHeader>
        {deployments.isError ? (
          <CardContent>
            <ErrorState title="Could not load App deployments" error={deployments.error} retry={() => deployments.refetch()} />
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

      {inspectedDeployment ? (
        <div className="mt-5 space-y-4">
          <Card>
            <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
              <div>
                <CardTitle>App build deployment v{inspectedDeployment.version}</CardTitle>
                <p className="mt-1 text-xs text-fog">Created {formatDate(inspectedDeployment.created_at)} · {inspectedDeployment.source_name ?? "Source archive"}</p>
              </div>
              <StatusBadge status={inspectedDeployment.build_status} />
            </CardHeader>
            <CardContent className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
              <Metric label="Source SHA-256"><span className="break-all font-mono text-[11px]">{inspectedDeployment.source_checksum_sha256}</span></Metric>
              <Metric label="Dockerfile">{inspectedDeployment.dockerfile_path}</Metric>
              <Metric label="Context directory">{inspectedDeployment.context_directory}</Metric>
              <Metric label="Target">{inspectedDeployment.target ?? "Default stage"}</Metric>
              <Metric label="Platform">{inspectedDeployment.platform}</Metric>
              <Metric label="WorkloadSpec snapshot">{inspectedDeployment.workload_snapshot.schema_version} · {inspectedDeployment.workload_spec_sha256.slice(0, 16)}…</Metric>
              <Metric label="Image digest"><span className="break-all font-mono text-[11px]">{inspectedDeployment.image_digest ?? "Not produced"}</span></Metric>
              <Metric label="OCI archive SHA-256"><span className="break-all font-mono text-[11px]">{inspectedDeployment.image_archive_sha256 ?? "Not produced"}</span></Metric>
              <Metric label="Image archive size">{inspectedDeployment.image_size_bytes ? formatBytes(inspectedDeployment.image_size_bytes) : "Not produced"}</Metric>
              <Metric label="Selection">{app.desired_deployment_id === inspectedDeployment.id ? "Desired image selected" : "Not selected"}</Metric>
              <Metric label="Build started">{inspectedDeployment.build_started_at ? formatDate(inspectedDeployment.build_started_at) : "Not started"}</Metric>
              <Metric label="Built">{inspectedDeployment.built_at ? formatDate(inspectedDeployment.built_at) : "Not built"}</Metric>
              {inspectedDeployment.error_message ? <Metric label="Build error">{inspectedDeployment.error_message}</Metric> : null}
            </CardContent>
          </Card>
          <LogViewer
            key={inspectedDeployment.id}
            title="Build logs"
            description="Bounded output from the trusted App build worker. Runtime logs are not available."
            source={buildLogSource}
            polling={inspectedDeployment.status === "queued" || inspectedDeployment.status === "building"}
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
              This removes the App configuration and releases its reserved hostname. No workload runtime exists to stop.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" disabled={remove.isPending} onClick={() => setDeleteOpen(false)}>Cancel</Button>
            <Button variant="destructive" disabled={remove.isPending} onClick={() => void onDelete()}>
              {remove.isPending ? "Deleting…" : "Delete App"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function Metric({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0 space-y-1">
      <p className="text-[10px] font-medium uppercase tracking-[0.12em] text-fog">{label}</p>
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
      cell: ({ row }) => <span className="font-mono text-xs">{row.original.platform}</span>,
    },
    {
      id: "image",
      header: "Image artifact",
      cell: ({ row }) => (
        <div className="max-w-56 space-y-1">
          <span className="block truncate font-mono text-[11px]" title={row.original.image_digest ?? undefined}>
            {row.original.image_digest ?? "Not produced"}
          </span>
          <span className="block text-[11px] text-fog">
            {row.original.image_size_bytes ? formatBytes(row.original.image_size_bytes) : "—"}
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
      cell: ({ row }) => <span className="whitespace-nowrap">{formatDate(row.original.created_at)}</span>,
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
                inspectedDeploymentId === row.original.id ? null : row.original.id,
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
