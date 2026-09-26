"use client";

import { useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Pencil, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { useDeleteApp, useUpdateApp } from "@/api/mutations";
import { useApp, useAppDeployments, useApps } from "@/api/queries";
import { createLogSource, LogViewer } from "@/components/log-viewer";
import { formatDate } from "@/lib/format";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { AppEditorDialog } from "@/features/apps/app-editor-dialog";
import { AppEnvironmentVariablesPanel } from "@/features/apps/app-environment-variables-panel";
import { AppOverviewPanel } from "@/features/apps/app-overview-panel";
import { AppDeploymentsPanel } from "@/features/apps/app-deployments-panel";
import { AppDeploymentDetailPanel } from "@/features/apps/app-deployment-detail-panel";
import { updateAppPayload, type AppFormValues } from "@/features/apps/app-form";
import { BackLink } from "@/features/resources/detail-shared";
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
  const [editOpen, setEditOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
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

      <AppOverviewPanel
        app={app}
        deploymentCount={deploymentItems.length}
        deploymentsPending={deployments.isPending}
      />

      <AppDeploymentsPanel
        projectId={projectId}
        appId={appId}
        app={app}
        deployments={deployments}
        deploymentNavigation={deploymentNavigation}
        inspectedDeploymentId={inspectedDeploymentId}
        setInspectedDeploymentId={setInspectedDeploymentId}
        canManage={canManage}
      />

      <AppEnvironmentVariablesPanel projectId={projectId} appId={appId} />

      <LogViewer
        key={`runtime:${app.id}`}
        title="Runtime logs"
        description="stdout/stderr captured from verified App containers and retained by the platform telemetry policy."
        source={runtimeLogSource}
        emptyMessage="No retained runtime log lines were returned for this App."
      />

      <AppDeploymentDetailPanel
        app={app}
        appId={appId}
        projectId={projectId}
        deployment={inspectedDeployment}
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
