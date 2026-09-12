"use client";
import type { ColumnDef } from "@tanstack/react-table";
import { useState, type FormEvent } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { toast } from "sonner";
import { apiUrl } from "@/api/client";
import { nextCursor } from "@/api/pagination";
import {
  useDeleteStorageFile,
  useUploadStorageFile,
  useRenameStorageFile,
  useUpdateStorageBucket,
} from "@/api/mutations";
import {
  useStorageBucket,
  useStorageFiles,
  useStorageFile,
} from "@/api/queries";
import type { StorageFile, StorageBucket } from "@/api/types";
import { DataTable } from "@/components/data-table";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { CreateDialog } from "@/components/create-dialog";
import { EmptyState } from "@/components/empty-state";
import { ErrorState, errorMessage } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { CopyButton } from "@/components/copy-button";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { formatBytes, formatDate } from "@/lib/format";
import { pageControls } from "@/lib/pagination";
import { BackLink } from "@/features/resources/detail-shared";
import { objectName } from "./storage-values";
import {
  storageBucketSettingsFields,
  storageBucketSettingsPayload,
  storageObjectFields,
  storageObjectPayload,
  type StorageBucketSettingsFormValues,
  type StorageObjectFormValues,
} from "@/features/storage/bucket-form";

function UploadObject({
  projectId,
  bucket,
  onUploaded,
  onClose,
}: {
  projectId: string;
  bucket: StorageBucket;
  onUploaded: (file?: StorageFile) => void;
  onClose: () => void;
}) {
  const upload = useUploadStorageFile(projectId, bucket.id);
  const [selected, setSelected] = useState<File>();
  const [validationError, setValidationError] = useState<string>();
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!selected || upload.isPending) return;
    try {
      objectName.parse(selected.name);
      if (selected.size > bucket.max_file_size_bytes)
        throw new Error(
          `The file exceeds this bucket's ${formatBytes(bucket.max_file_size_bytes)} per-object limit.`,
        );
      if (selected.size > bucket.quota_bytes - bucket.used_bytes)
        throw new Error("The file exceeds this bucket's remaining quota.");
      setValidationError(undefined);
    } catch (error) {
      setValidationError(errorMessage(error));
      return;
    }
    try {
      const result = await upload.mutateAsync(selected);
      toast.success("Object uploaded");
      onUploaded(result?.file);
    } catch (error) {
      toast.error(errorMessage(error));
    }
  };
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !upload.isPending) onClose();
      }}
    >
      <DialogContent showClose={!upload.isPending}>
        <DialogHeader>
          <DialogTitle>Upload object</DialogTitle>
          <DialogDescription>
            Choose one file. Its filename becomes the object name; folders and
            paths are not supported.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <label className="grid gap-2 text-sm">
            Choose file
            <Input
              type="file"
              aria-label="Choose file"
              disabled={upload.isPending}
              onChange={(event) => {
                setSelected(event.target.files?.[0]);
                setValidationError(undefined);
                upload.reset();
              }}
            />
          </label>
          {selected ? (
            <div className="rounded-lg border border-stealth-border p-3 text-sm">
              <p className="break-all">{selected.name}</p>
              <p className="mt-1 text-slate-500">
                {formatBytes(selected.size)}
              </p>
            </div>
          ) : null}
          <p className="text-xs text-slate-500">
            Maximum object size: {formatBytes(bucket.max_file_size_bytes)}.
            Available quota:{" "}
            {formatBytes(Math.max(0, bucket.quota_bytes - bucket.used_bytes))}.
          </p>
          {validationError ? (
            <p role="alert" className="text-sm text-rose-300">
              {validationError}
            </p>
          ) : null}
          {upload.error ? (
            <ErrorState title="Could not upload object" error={upload.error} />
          ) : null}
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              disabled={upload.isPending}
              onClick={onClose}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={!selected || upload.isPending}>
              {upload.isPending ? "Uploading object…" : "Upload object"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function ObjectDetail({
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

export function BucketDetailView({
  organizationId,
  projectId,
  bucketId,
}: {
  organizationId: string;
  projectId: string;
  bucketId: string;
}) {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const bucket = useStorageBucket(projectId, bucketId);
  const filesNavigation = useCursorPagination("files_cursor");
  const files = useStorageFiles(projectId, bucketId, {
    cursor: filesNavigation.cursor,
  });
  const remove = useDeleteStorageFile(projectId, bucketId);
  const update = useUpdateStorageBucket(projectId, bucketId);
  const [uploadOpen, setUploadOpen] = useState(false);
  const [tab, setTab] = useState("objects");
  const current = bucket.data?.bucket;
  const canManage = files.data?.can_manage === true;
  const selectObject = (fileId?: string) => {
    const next = new URLSearchParams(searchParams.toString());
    if (fileId) next.set("file_id", fileId);
    else next.delete("file_id");
    router.replace(`${pathname}${next.size ? `?${next}` : ""}`, {
      scroll: false,
    });
  };
  if (bucket.error && !current)
    return (
      <ErrorState
        title="Could not load bucket"
        error={bucket.error}
        retry={() => bucket.refetch()}
      />
    );
  if (bucket.isPending) return <LoadingState />;
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
        <button
          className="block max-w-64 truncate text-left font-medium text-white hover:text-cyan-200"
          title={row.original.name}
          onClick={() => selectObject(row.original.id)}
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
                `/v1/projects/${projectId}/storage/buckets/${bucketId}/files/${row.original.id}/download`,
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
                  selectObject();
              }}
            />
          ) : null}
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
        description="Browse files, inspect metadata, and manage storage limits."
        actions={
          canManage ? (
            <Button onClick={() => setUploadOpen(true)}>Upload object</Button>
          ) : undefined
        }
      />
      <div className="mb-5 flex flex-wrap items-center gap-3 text-xs text-slate-500">
        <ResourceId id={current.id} label="Bucket ID" />
        <Badge variant="neutral">
          File security {current.file_security ? "enabled" : "disabled"}
        </Badge>
        <span>
          {formatBytes(current.used_bytes)} / {formatBytes(current.quota_bytes)}{" "}
          used
        </span>
        <span>Created {formatDate(current.created_at)}</span>
        <span>Updated {formatDate(current.updated_at)}</span>
      </div>
      {bucket.error ? (
        <ErrorState
          title="Could not refresh bucket"
          error={bucket.error}
          retry={() => bucket.refetch()}
        />
      ) : null}
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="objects">Objects</TabsTrigger>
          <TabsTrigger value="settings">Settings</TabsTrigger>
        </TabsList>
        <TabsContent value="objects">
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
                action={() => setUploadOpen(true)}
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
        </TabsContent>
        <TabsContent value="settings">
          <Card>
            <CardHeader>
              <CardTitle>Bucket settings</CardTitle>
            </CardHeader>
            <CardContent className="space-y-5">
              <dl className="grid gap-4 sm:grid-cols-2">
                <div>
                  <dt className="text-xs text-slate-500">
                    Maximum object size
                  </dt>
                  <dd>{formatBytes(current.max_file_size_bytes)}</dd>
                </div>
                <div>
                  <dt className="text-xs text-slate-500">Quota</dt>
                  <dd>{formatBytes(current.quota_bytes)}</dd>
                </div>
              </dl>
              <p className="text-sm text-slate-400">
                {current.file_security
                  ? "Bucket or individual file grants can allow access."
                  : "Only bucket grants control file access."}{" "}
                File security alone does not determine public visibility.
              </p>
              <dl className="space-y-2">
                {(
                  [
                    "create_permissions",
                    "read_permissions",
                    "update_permissions",
                    "delete_permissions",
                  ] as const
                ).map((key) => (
                  <div key={key}>
                    <dt className="text-xs text-slate-500">
                      {key.replace("_", " ")}
                    </dt>
                    <dd className="break-all font-mono text-xs">
                      {JSON.stringify(current[key])}
                    </dd>
                  </div>
                ))}
              </dl>
              {canManage ? (
                <CreateDialog<StorageBucketSettingsFormValues>
                  key={current.updated_at}
                  triggerLabel="Edit bucket settings"
                  submitLabel="Save settings"
                  pendingLabel="Saving settings…"
                  title="Edit bucket settings"
                  description="Update the name and size limits. Existing access permissions stay unchanged. The server enforces its global upload limit."
                  fields={storageBucketSettingsFields({
                    name: current.name,
                    max_file_size_bytes: String(current.max_file_size_bytes),
                    quota_bytes: String(current.quota_bytes),
                  })}
                  pending={update.isPending}
                  onSubmit={async (values) => {
                    await update.mutateAsync(
                      storageBucketSettingsPayload(values, current.used_bytes),
                    );
                    toast.success("Bucket settings updated");
                  }}
                />
              ) : null}
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
      {uploadOpen ? (
        <UploadObject
          projectId={projectId}
          bucket={current}
          onClose={() => setUploadOpen(false)}
          onUploaded={(file) => {
            setUploadOpen(false);
            setTab("objects");
            if (file?.id) selectObject(file.id);
            else filesNavigation.goFirst();
          }}
        />
      ) : null}
      {searchParams.get("file_id") ? (
        <ObjectDetail
          projectId={projectId}
          bucketId={bucketId}
          fileId={searchParams.get("file_id")!}
          canManage={canManage}
          onClose={() => selectObject()}
        />
      ) : null}
    </>
  );
}
