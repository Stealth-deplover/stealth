"use client";

import { toast } from "sonner";
import { apiUrl } from "@/api/client";
import { useRenameStorageFile } from "@/api/mutations";
import { useStorageFile } from "@/api/queries";
import { CreateDialog } from "@/components/create-dialog";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { CopyButton } from "@/components/copy-button";
import { ResourceId } from "@/components/resource-id";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { formatBytes, formatDate } from "@/lib/format";
import {
  storageObjectFields,
  storageObjectPayload,
  type StorageObjectFormValues,
} from "@/features/storage/bucket-form";

export function BucketObjectDetail({
  projectId,
  bucketId,
  fileId,
  canManage,
  onClose,
}: {
  projectId: string;
  bucketId: string;
  fileId: string;
  canManage: boolean;
  onClose: () => void;
}) {
  const query = useStorageFile(projectId, bucketId, fileId);
  const rename = useRenameStorageFile(projectId, bucketId);
  const file = query.data?.file;

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Object detail</DialogTitle>
          <DialogDescription>
            File metadata and verified content checksum.
          </DialogDescription>
        </DialogHeader>
        {query.error ? (
          <ErrorState
            title="Could not load object metadata"
            error={query.error}
            retry={() => query.refetch()}
          />
        ) : null}
        {query.isPending ? (
          <LoadingState />
        ) : file ? (
          <div className="space-y-5">
            <div className="flex items-center gap-2">
              <span className="break-all font-medium">{file.name}</span>
              <CopyButton value={file.name} label="Copy object name" />
            </div>
            <ResourceId id={file.id} label="Object ID" />
            <dl className="grid gap-4 sm:grid-cols-2">
              <div>
                <dt className="text-xs text-slate-500">Content type</dt>
                <dd className="mt-1 break-all text-sm">{file.mime_type}</dd>
              </div>
              <div>
                <dt className="text-xs text-slate-500">Size</dt>
                <dd className="mt-1 text-sm">{formatBytes(file.size_bytes)}</dd>
              </div>
              <div>
                <dt className="text-xs text-slate-500">Created</dt>
                <dd className="mt-1 text-sm">{formatDate(file.created_at)}</dd>
              </div>
              <div>
                <dt className="text-xs text-slate-500">Updated</dt>
                <dd className="mt-1 text-sm">{formatDate(file.updated_at)}</dd>
              </div>
              <div className="sm:col-span-2">
                <dt className="text-xs text-slate-500">SHA-256</dt>
                <dd className="mt-1 flex items-center gap-2">
                  <code className="break-all text-xs">
                    {file.checksum_sha256}
                  </code>
                  <CopyButton
                    value={file.checksum_sha256}
                    label="Copy checksum"
                  />
                </dd>
              </div>
            </dl>
            <div className="flex gap-2">
              <Button asChild variant="outline">
                <a
                  href={apiUrl(
                    `/v1/projects/${projectId}/storage/buckets/${bucketId}/files/${file.id}/download`,
                  )}
                >
                  Download object
                </a>
              </Button>
              {canManage ? (
                <CreateDialog<StorageObjectFormValues>
                  key={file.updated_at}
                  triggerLabel="Rename object"
                  submitLabel="Save name"
                  pendingLabel="Saving name…"
                  title="Rename object"
                  description="Change the display name. The object ID and file contents stay the same."
                  fields={storageObjectFields(file.name)}
                  pending={rename.isPending}
                  onSubmit={async (values) => {
                    await rename.mutateAsync({
                      fileId: file.id,
                      body: storageObjectPayload(values),
                    });
                    toast.success("Object renamed");
                  }}
                />
              ) : null}
            </div>
          </div>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
