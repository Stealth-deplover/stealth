"use client";
import Link from "next/link";
import { type ColumnDef } from "@tanstack/react-table";
import { HardDrive } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { useCreateBucket } from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { useStorageBuckets } from "@/api/queries";
import type { StorageBucket } from "@/api/types";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { formatBytes, formatDate } from "@/lib/format";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { ProjectResourceIntro } from "@/features/resources/collection-shared";
import { pageControls } from "@/lib/pagination";

export function StorageView({
  organizationId,
  projectId,
}: {
  organizationId: string;
  projectId: string;
}) {
  const navigation = useCursorPagination();
  const [createOpen, setCreateOpen] = useState(false);
  const query = useStorageBuckets(projectId, { cursor: navigation.cursor });
  const create = useCreateBucket(projectId);
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const handleCreateBucket = async (values: Record<string, string>) => {
    await create.mutateAsync({
      name: values.name,
      file_security: true,
    });
    toast.success("Bucket created");
  };
  const columns: ColumnDef<StorageBucket, unknown>[] = [
    {
      accessorKey: "name",
      header: "Bucket",
      cell: ({ row }) => (
        <Link
          href={`${base}/storage/${row.original.id}`}
          className="font-medium text-white hover:text-emerald-200"
        >
          {row.original.name}
        </Link>
      ),
    },
    {
      accessorKey: "used_bytes",
      header: "Used",
      cell: ({ row }) =>
        `${formatBytes(row.original.used_bytes)} / ${formatBytes(row.original.quota_bytes)}`,
    },
    {
      accessorKey: "file_security",
      header: "File security",
      cell: ({ row }) => (
        <Badge variant={row.original.file_security ? "success" : "warning"}>
          {row.original.file_security ? "Enabled" : "Disabled"}
        </Badge>
      ),
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
        eyebrow="Data"
        title="Storage"
        description="Flat object storage for files and artifacts, with backend-defined permissions."
        actions={
          <CreateDialog
            open={createOpen}
            onOpenChange={setCreateOpen}
            triggerLabel="Create bucket"
            submitLabel="Create bucket"
            pendingLabel="Creating bucket…"
            title="Create a bucket"
            description="Bucket names are lowercase and hyphenated. Folders are not modeled by this API."
            fields={[{ name: "name", label: "Name", placeholder: "assets" }]}
            pending={create.isPending}
            onSubmit={handleCreateBucket}
          />
        }
      />
      <ProjectResourceIntro
        icon={HardDrive}
        title="Buckets"
        description="Files are stored as safe object names. The console keeps the explorer flat because the backend does not expose folder semantics."
      />
      {query.isError ? (
        <ErrorState error={query.error} retry={() => query.refetch()} />
      ) : query.data?.buckets.length ? (
        <Card>
          <DataTable
            data={query.data.buckets}
            columns={columns}
            serverPagination={pageControls(
              navigation,
              nextCursor(query.data),
              query.isFetching,
            )}
          />
        </Card>
      ) : query.isLoading ? (
        <Card>
          <DataTable data={[]} columns={columns} loading />
        </Card>
      ) : (
        <EmptyState
          title="No buckets yet"
          description="Create a bucket for user uploads, public assets, or build artifacts."
          actionLabel="Create bucket"
          action={() => setCreateOpen(true)}
        />
      )}
    </>
  );
}
