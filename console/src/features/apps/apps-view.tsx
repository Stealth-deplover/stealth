"use client";

import Link from "next/link";
import { AppWindow } from "lucide-react";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";
import { useCreateApp } from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { useApps } from "@/api/queries";
import type { StealthApp } from "@/api/types";
import { DataTable, type DataTableColumnDef } from "@/components/data-table";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { ProjectResourceIntro } from "@/features/resources/collection-shared";
import { AppEditorDialog } from "@/features/apps/app-editor-dialog";
import { createAppPayload, type AppFormValues } from "@/features/apps/app-form";
import { pageControls } from "@/lib/pagination";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { formatBytes, formatDate } from "@/lib/format";
import { ResourceTableCard } from "@/components/resource-page";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";

export function AppsView({
  organizationId,
  projectId,
}: {
  organizationId: string;
  projectId: string;
}) {
  const router = useRouter();
  const navigation = useCursorPagination();
  const [createOpen, setCreateOpen] = useState(false);
  const query = useApps(projectId, { cursor: navigation.cursor });
  const create = useCreateApp(projectId);
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const canManage = query.data?.can_manage === true;

  const onCreate = async (values: AppFormValues) => {
    const result = await create.mutateAsync(createAppPayload(values));
    toast.success("App configuration saved");
    if (!result?.app?.id) throw new Error("The API did not return the created App.");
    router.push(`${base}/apps/${result.app.id}`);
  };

  const columns: DataTableColumnDef<StealthApp>[] = [
    {
      accessorKey: "name",
      header: "App",
      cell: ({ row }) => (
        <Link
          href={`${base}/apps/${row.original.id}`}
          className="font-medium text-white hover:text-amber-200"
        >
          <span className="block">{row.original.name}</span>
          <span className="mt-0.5 block font-mono text-[11px] text-fog">
            {row.original.platform_hostname ?? "Hostname not configured"}
          </span>
        </Link>
      ),
    },
    {
      accessorKey: "enabled",
      header: "Desired state",
      cell: ({ row }) => (
        <Badge variant="neutral">
          {row.original.enabled ? "Enabled" : "Disabled"}
        </Badge>
      ),
    },
    {
      accessorKey: "runtime_status",
      header: "Runtime",
      cell: ({ row }) => <StatusBadge status={row.original.runtime_status} />,
    },
    {
      id: "resources",
      header: "Resources",
      cell: ({ row }) => (
        <span className="whitespace-nowrap text-xs text-mist">
          {row.original.workload.resources.cpu_millis} mCPU · {formatBytes(row.original.workload.resources.memory_bytes)}
        </span>
      ),
    },
    {
      accessorKey: "workload.port",
      header: "Internal port",
      cell: ({ row }) => <span className="font-mono text-xs">:{row.original.workload.port}</span>,
    },
    {
      accessorKey: "updated_at",
      header: "Updated",
      cell: ({ row }) => formatDate(row.original.updated_at),
    },
  ];

  return (
    <>
      <PageHeader
        eyebrow="Compute"
        title="Apps"
        description="Persistent project workloads with explicit, versioned runtime intent."
        actions={
          canManage ? (
            <Button onClick={() => setCreateOpen(true)}>
              <AppWindow className="size-4" aria-hidden="true" /> Create App
            </Button>
          ) : query.data ? (
            <Badge variant="neutral">Read-only</Badge>
          ) : null
        }
      />
      <AppEditorDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        pending={create.isPending}
        onSubmit={onCreate}
      />
      <ProjectResourceIntro
        icon={AppWindow}
        title="Persistent workloads"
        description="This control plane stores desired runtime configuration. No image, running process, or public App route exists yet."
      />
      {query.isError ? (
        <ErrorState title="Could not load Apps" error={query.error} retry={() => query.refetch()} />
      ) : query.data?.apps.length ? (
        <ResourceTableCard
          data={query.data.apps}
          searchable={(item, term) =>
            `${item.name} ${item.runtime_status} ${item.platform_hostname ?? ""}`.toLowerCase().includes(term)
          }
          serverPagination={pageControls(
            navigation,
            nextCursor(query.data),
            query.isFetching,
          )}
        >
          {(filtered, pagination) => (
            <DataTable data={filtered} columns={columns} serverPagination={pagination} />
          )}
        </ResourceTableCard>
      ) : query.isLoading ? (
        <Card>
          <DataTable data={[]} columns={columns} loading />
        </Card>
      ) : (
        <EmptyState
          icon={<AppWindow className="size-5" aria-hidden="true" />}
          title="No Apps yet"
          description={canManage ? "Create an App to save its desired workload settings. Its runtime status will remain Not deployed until build and execution support arrive." : "No Apps are available in this project."}
          actionLabel={canManage ? "Create App" : undefined}
          action={canManage ? () => setCreateOpen(true) : undefined}
        />
      )}
    </>
  );
}
