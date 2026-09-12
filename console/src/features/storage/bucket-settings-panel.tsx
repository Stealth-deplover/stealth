"use client";

import { toast } from "sonner";
import type { StorageBucket } from "@/api/types";
import { useUpdateStorageBucket } from "@/api/mutations";
import { CreateDialog } from "@/components/create-dialog";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { formatBytes } from "@/lib/format";
import {
  storageBucketSettingsFields,
  storageBucketSettingsPayload,
  type StorageBucketSettingsFormValues,
} from "@/features/storage/bucket-form";

export function BucketSettingsPanel({
  projectId,
  bucket,
  canManage,
}: {
  projectId: string;
  bucket: StorageBucket;
  canManage: boolean;
}) {
  const update = useUpdateStorageBucket(projectId, bucket.id);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Bucket settings</CardTitle>
      </CardHeader>
      <CardContent className="space-y-5">
        <dl className="grid gap-4 sm:grid-cols-2">
          <div>
            <dt className="text-xs text-slate-500">Maximum object size</dt>
            <dd>{formatBytes(bucket.max_file_size_bytes)}</dd>
          </div>
          <div>
            <dt className="text-xs text-slate-500">Quota</dt>
            <dd>{formatBytes(bucket.quota_bytes)}</dd>
          </div>
        </dl>
        <p className="text-sm text-slate-400">
          {bucket.file_security
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
                {JSON.stringify(bucket[key])}
              </dd>
            </div>
          ))}
        </dl>
        {canManage ? (
          <CreateDialog<StorageBucketSettingsFormValues>
            key={bucket.updated_at}
            triggerLabel="Edit bucket settings"
            submitLabel="Save settings"
            pendingLabel="Saving settings…"
            title="Edit bucket settings"
            description="Update the name and size limits. Existing access permissions stay unchanged. The server enforces its global upload limit."
            fields={storageBucketSettingsFields({
              name: bucket.name,
              max_file_size_bytes: String(bucket.max_file_size_bytes),
              quota_bytes: String(bucket.quota_bytes),
            })}
            pending={update.isPending}
            onSubmit={async (values) => {
              await update.mutateAsync(
                storageBucketSettingsPayload(values, bucket.used_bytes),
              );
              toast.success("Bucket settings updated");
            }}
          />
        ) : null}
      </CardContent>
    </Card>
  );
}
