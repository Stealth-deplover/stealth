"use client";

// Bucket detail is the composition shell. Object browsing/upload, object
// metadata, and settings have separate owners so storage lifecycle changes do
// not accumulate in one route component.
import { useState } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useStorageBucket, useStorageFiles } from "@/api/queries";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { formatBytes, formatDate } from "@/lib/format";
import { BackLink } from "@/features/resources/detail-shared";
import { BucketObjectBrowser } from "./bucket-object-browser";
import { BucketObjectDetail } from "./bucket-object-detail";
import { BucketSettingsPanel } from "./bucket-settings-panel";

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
  // The list response is also the authoritative permission projection. The
  // object browser owns its paginated query; this stable first-page query
  // lets the shell keep header/settings actions permission-aware.
  const access = useStorageFiles(projectId, bucketId);
  const [uploadOpen, setUploadOpen] = useState(false);
  const [tab, setTab] = useState("objects");
  const current = bucket.data?.bucket;
  const canManage = access.data?.can_manage === true;

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
      {access.error ? (
        <ErrorState
          title="Could not load bucket access"
          error={access.error}
          retry={() => access.refetch()}
        />
      ) : null}
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="objects">Objects</TabsTrigger>
          <TabsTrigger value="settings">Settings</TabsTrigger>
        </TabsList>
        <TabsContent value="objects">
          <BucketObjectBrowser
            projectId={projectId}
            bucket={current}
            canManage={canManage}
            uploadOpen={uploadOpen}
            onUploadOpenChange={setUploadOpen}
            onUploadFinished={() => setTab("objects")}
            onSelectObject={selectObject}
          />
        </TabsContent>
        <TabsContent value="settings">
          <BucketSettingsPanel
            projectId={projectId}
            bucket={current}
            canManage={canManage}
          />
        </TabsContent>
      </Tabs>
      {searchParams.get("file_id") ? (
        <BucketObjectDetail
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
