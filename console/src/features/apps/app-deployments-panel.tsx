import { useState } from "react";
import { FileUp } from "lucide-react";
import { toast } from "sonner";
import {
  useCreateAppDeployment,
  useSelectAppDeployment,
} from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import type { useAppDeployments } from "@/api/queries";
import { DataTable, type DataTableColumnDef } from "@/components/data-table";
import { formatBytes, formatDate } from "@/lib/format";
import { ErrorState } from "@/components/feedback/error-state";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { AppDeploymentDialog } from "@/features/apps/app-deployment-dialog";
import type { AppDeployment, StealthApp } from "@/api/types";
import { pageControls } from "@/lib/pagination";
import type { useCursorPagination } from "@/hooks/use-cursor-pagination";

type DeploymentsQuery = ReturnType<typeof useAppDeployments>;
type DeploymentNavigation = ReturnType<typeof useCursorPagination>;

export function AppDeploymentsPanel({
  projectId,
  appId,
  app,
  deployments,
  deploymentNavigation,
  inspectedDeploymentId,
  setInspectedDeploymentId,
  canManage,
}: {
  projectId: string;
  appId: string;
  app: StealthApp;
  deployments: DeploymentsQuery;
  deploymentNavigation: DeploymentNavigation;
  inspectedDeploymentId: string | null;
  setInspectedDeploymentId: (id: string | null) => void;
  canManage: boolean;
}) {
  const createDeployment = useCreateAppDeployment(projectId, appId);
  const selectDeployment = useSelectAppDeployment(projectId, appId);
  const [deploymentOpen, setDeploymentOpen] = useState(false);
  const deploymentItems = deployments.data?.deployments ?? [];

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

  return (
    <>
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

      <AppDeploymentDialog
        open={deploymentOpen}
        onOpenChange={setDeploymentOpen}
        pending={createDeployment.isPending}
        onSubmit={onCreateDeployment}
      />
    </>
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
