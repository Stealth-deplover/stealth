"use client";
import type { ColumnDef } from "@tanstack/react-table";
import { Download, FileUp, HardDrive, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { apiUrl, uploadMultipart } from "@/api/client";
import { nextCursor } from "@/api/pagination";
import { useDeleteStorageFile } from "@/api/mutations";
import { useStorageBucket, useStorageFiles } from "@/api/queries";
import type { StorageFile } from "@/api/types";
import { DataTable } from "@/components/data-table";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { Button } from "@/components/ui/button";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { formatBytes, formatDate } from "@/lib/format";
import { pageControls } from "@/lib/pagination";
import { BackLink } from "@/features/resources/detail-shared";

export function BucketDetailView({
  organizationId,
  projectId,
  bucketId,
}: {
  organizationId: string;
  projectId: string;
  bucketId: string;
}) {
  const bucket = useStorageBucket(projectId, bucketId);
  const filesNavigation = useCursorPagination("files_cursor");
  const files = useStorageFiles(projectId, bucketId, {
    cursor: filesNavigation.cursor,
  });
  const removeFile = useDeleteStorageFile(projectId, bucketId);
  const [uploading, setUploading] = useState(false);
  const current = bucket.data?.bucket;
  const upload = async (file: File) => {
    setUploading(true);
    try {
      const form = new FormData();
      form.append("file", file);
      await uploadMultipart(
        `/v1/projects/${projectId}/storage/buckets/${bucketId}/files`,
        form,
      );
      toast.success("File uploaded");
      await files.refetch();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Upload failed");
    } finally {
      setUploading(false);
    }
  };
  if (bucket.error)
    return <ErrorState error={bucket.error} retry={() => bucket.refetch()} />;
  if (files.error)
    return <ErrorState error={files.error} retry={() => files.refetch()} />;
  if (!current)
    return (
      <EmptyState
        title="Bucket not found"
        description="The bucket may have been removed or is outside this project."
      />
    );
  const columns: ColumnDef<StorageFile, unknown>[] = [
    {
      accessorKey: "name",
      header: "Name",
      cell: ({ row }) => (
        <span className="font-medium text-white">{row.original.name}</span>
      ),
    },
    {
      accessorKey: "mime_type",
      header: "Type",
      cell: ({ row }) => (
        <span className="text-xs text-slate-400">{row.original.mime_type}</span>
      ),
    },
    {
      accessorKey: "size_bytes",
      header: "Size",
      cell: ({ row }) => formatBytes(row.original.size_bytes),
    },
    {
      accessorKey: "updated_at",
      header: "Modified",
      cell: ({ row }) => formatDate(row.original.updated_at),
    },
    {
      id: "actions",
      header: "",
      cell: ({ row }) => (
        <div className="flex items-center justify-end gap-1">
          <Button
            asChild
            variant="ghost"
            size="icon"
            aria-label={`Download ${row.original.name}`}
          >
            <a
              href={apiUrl(
                `/v1/projects/${projectId}/storage/buckets/${bucketId}/files/${row.original.id}/download`,
              )}
            >
              <Download className="size-3.5" />
            </a>
          </Button>
          <ConfirmDialog
            trigger={
              <Button
                variant="ghost"
                size="icon"
                aria-label={`Delete ${row.original.name}`}
              >
                <Trash2 className="size-3.5 text-rose-300" />
              </Button>
            }
            title="Delete this file?"
            description={`The object ${row.original.name} will be removed from the bucket. This cannot be undone.`}
            confirmLabel="Delete file"
            pending={removeFile.isPending}
            onConfirm={async () => {
              await removeFile.mutateAsync(row.original.id);
              toast.success("File deleted");
            }}
          />
        </div>
      ),
    },
  ];
  return (
    <>
      <BackLink
        href={`/organizations/${organizationId}/projects/${projectId}/storage`}
        label="Back to storage"
      />
      <PageHeader
        eyebrow="Storage bucket"
        title={current.name}
        description="A flat object explorer backed by the storage files API."
        actions={
          <label className="inline-flex h-9 cursor-pointer items-center gap-2 rounded-lg bg-cyan-300 px-3.5 text-sm font-medium text-slate-950 hover:bg-cyan-200">
            <FileUp className="size-4" />{" "}
            {uploading ? "Uploading…" : "Upload file"}
            <input
              type="file"
              className="sr-only"
              disabled={uploading}
              onChange={(event) => {
                const file = event.target.files?.[0];
                event.currentTarget.value = "";
                if (file) void upload(file);
              }}
            />
          </label>
        }
      />
      <div className="mb-5 flex flex-wrap items-center gap-2">
        <span className="text-xs text-slate-600">
          {formatBytes(current.used_bytes)} of{" "}
          {formatBytes(current.quota_bytes)} used
        </span>
        <span className="text-xs text-slate-600">
          Updated {formatDate(current.updated_at)}
        </span>
        <ResourceId id={current.id} label="Bucket ID" />
      </div>
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <HardDrive className="size-4 text-emerald-300" /> Files
          </CardTitle>
        </CardHeader>
        <DataTable
          data={files.data?.files ?? []}
          columns={columns}
          loading={files.isLoading}
          empty="No files yet. Upload an object to start using this bucket."
          serverPagination={pageControls(
            filesNavigation,
            nextCursor(files.data),
            files.isFetching,
          )}
        />
      </Card>
    </>
  );
}
