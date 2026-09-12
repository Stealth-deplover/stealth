"use client";

import type { ColumnDef } from "@tanstack/react-table";
import { useSearchParams } from "next/navigation";
import { toast } from "sonner";
import { apiUrl } from "@/api/client";
import { nextCursor } from "@/api/pagination";
import { useDeleteStorageFile } from "@/api/mutations";
import { useStorageFiles } from "@/api/queries";
import type { StorageBucket, StorageFile } from "@/api/types";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DataTable } from "@/components/data-table";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { Button } from "@/components/ui/button";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { formatBytes, formatDate } from "@/lib/format";
import { pageControls } from "@/lib/pagination";
import { BucketUploadDialog } from "./bucket-upload-dialog";

export function BucketObjectBrowser({
  projectId,
  bucket,
  canManage,
  uploadOpen,
  onUploadOpenChange,
  onUploadFinished,
  onSelectObject,
}: {
  projectId: string;
  bucket: StorageBucket;
  canManage: boolean;
  uploadOpen: boolean;
  onUploadOpenChange: (open: boolean) => void;
  onUploadFinished: () => void;
  onSelectObject: (fileId?: string) => void;
}) {
  const searchParams = useSearchParams();
  const filesNavigation = useCursorPagination("files_cursor");
  const files = useStorageFiles(projectId, bucket.id, {
    cursor: filesNavigation.cursor,
  });
  const remove = useDeleteStorageFile(projectId, bucket.id);

  const columns: ColumnDef<StorageFile, unknown>[] = [
    {
      accessorKey: "name",
      header: "Name",
      cell: ({ row }) => (
        <button
          className="block max-w-64 truncate text-left font-medium text-white hover:text-cyan-200"
          title={row.original.name}
          onClick={() => onSelectObject(row.original.id)}
        >
          {row.original.name}
        </button>
      ),
    },
    {
      accessorKey: "mime_type",
      header: "Content type",
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
          <Button asChild variant="ghost" size="sm">
            <a
              aria-label={`Download ${row.original.name}`}
              href={apiUrl(
                `/v1/projects/${projectId}/storage/buckets/${bucket.id}/files/${row.original.id}/download`,
              )}
            >
              Download
            </a>
          </Button>
          {canManage ? (
            <ConfirmDialog
              trigger={
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={`Delete ${row.original.name}`}
                >
                  Delete
                </Button>
              }
              title={`Delete "${row.original.name}"?`}
              description="This permanently removes the object from this bucket."
              confirmLabel="Delete object"
              pending={remove.isPending}
              onConfirm={async () => {
                await remove.mutateAsync(row.original.id);
                toast.success("Object deleted");
                if (searchParams.get("file_id") === row.original.id)
                  onSelectObject();
              }}
            />
          ) : null}
        </div>
      ),
    },
  ];

  const handleUploaded = (file?: StorageFile) => {
    onUploadOpenChange(false);
    if (file?.id) onSelectObject(file.id);
    else filesNavigation.goFirst();
    onUploadFinished();
  };

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>Objects</CardTitle>
        </CardHeader>
        {files.error ? (
          <ErrorState
            title="Could not load bucket objects"
            error={files.error}
            retry={() => files.refetch()}
          />
        ) : null}
        {!files.isPending &&
        !files.error &&
        !files.data?.files.length &&
        !nextCursor(files.data) &&
        !filesNavigation.canFirst ? (
          <EmptyState
            title="This bucket is empty"
            description="Upload an object to start storing files."
            actionLabel={canManage ? "Upload object" : undefined}
            action={canManage ? () => onUploadOpenChange(true) : undefined}
          />
        ) : (
          <DataTable
            data={files.data?.files ?? []}
            columns={columns}
            loading={files.isPending}
            empty="No objects on this page."
            serverPagination={pageControls(
              filesNavigation,
              nextCursor(files.data),
              files.isFetching,
            )}
          />
        )}
      </Card>
      {uploadOpen ? (
        <BucketUploadDialog
          projectId={projectId}
          bucket={bucket}
          onClose={() => onUploadOpenChange(false)}
          onUploaded={handleUploaded}
        />
      ) : null}
    </>
  );
}
