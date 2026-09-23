"use client";

import { useState, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import { AppWindow, Pencil, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { useDeleteApp, useUpdateApp } from "@/api/mutations";
import { useApp, useApps } from "@/api/queries";
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
import { updateAppPayload, type AppFormValues } from "@/features/apps/app-form";
import { BackLink } from "@/features/resources/detail-shared";

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
  const update = useUpdateApp(projectId, appId);
  const remove = useDeleteApp(projectId, appId);
  const [editOpen, setEditOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const app = query.data?.app;
  const canManage = access.data?.can_manage === true;
  const base = `/organizations/${organizationId}/projects/${projectId}`;

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

      <Card className="mb-5 border-cyan-300/20 bg-cyan-300/[0.03]">
        <CardContent className="flex items-start gap-3 p-4">
          <AppWindow className="mt-0.5 size-4 shrink-0 text-cyan-200" aria-hidden="true" />
          <div>
            <p className="text-sm font-medium text-cyan-100">No deployment has been created for this App yet.</p>
            <p className="mt-1 text-xs leading-5 text-cyan-100/70">
              The saved configuration is not a running workload. Build and execution support will arrive in later capabilities.
            </p>
          </div>
        </CardContent>
      </Card>

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
