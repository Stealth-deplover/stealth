"use client";
import Link from "next/link";
import { useRouter } from "next/navigation";
import type { ColumnDef } from "@tanstack/react-table";
import { FileUp } from "lucide-react";
import { useCallback, useRef } from "react";
import { toast } from "sonner";
import { api, unwrap } from "@/api/client";
import { nextCursor } from "@/api/pagination";
import {
  useActivateSiteDeployment,
  useUploadSiteDeployment,
} from "@/api/mutations";
import { useSite, useSiteDeployment, useSiteDeployments } from "@/api/queries";
import type { SiteDeployment } from "@/api/types";
import { DataTable } from "@/components/data-table";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { LogViewer, type LogLine } from "@/components/log-viewer";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { formatBytes, formatDate, formatDuration } from "@/lib/format";
import {
  deploymentDurationMs,
  getDeploymentLifecycleStatus,
  isDeploymentInProgress,
} from "@/lib/deployment-state";
import { pageControls } from "@/lib/pagination";
import { BackLink } from "@/features/resources/detail-shared";

export function SiteDetailView({
  organizationId,
  projectId,
  siteId,
}: {
  organizationId: string;
  projectId: string;
  siteId: string;
}) {
  const query = useSite(projectId, siteId);
  const router = useRouter();
  const deploymentsNavigation = useCursorPagination("site_deployments_cursor");
  const deployments = useSiteDeployments(projectId, siteId, {
    cursor: deploymentsNavigation.cursor,
  });
  const upload = useUploadSiteDeployment(projectId, siteId);
  const activate = useActivateSiteDeployment(projectId, siteId);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const site = query.data?.site;
  const canManage = deployments.data?.can_manage === true;
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const openFilePicker = () => fileInputRef.current?.click();
  const handleDeploymentUpload = (file: File) => {
    upload.mutate(
      { file },
      {
        onSuccess: (result) => {
          if (result?.deployment.id) {
            router.push(
              `${base}/sites/${siteId}/deployments/${result.deployment.id}`,
            );
          } else {
            deploymentsNavigation.goFirst();
          }
          toast.success("Site deployment uploaded");
        },
        onError: () => toast.error("Could not upload site deployment"),
      },
    );
  };
  if (query.error)
    return <ErrorState error={query.error} retry={() => query.refetch()} />;
  if (deployments.error)
    return (
      <ErrorState
        error={deployments.error}
        retry={() => deployments.refetch()}
      />
    );
  if (!site)
    return (
      <EmptyState
        title="Site not found"
        description="The site may have been removed or is outside this project."
      />
    );
  const showFirstDeployment =
    !deploymentsNavigation.cursor && deployments.data?.deployments.length === 0;
  const columns: ColumnDef<SiteDeployment, unknown>[] = [
    {
      accessorKey: "version",
      header: "Version",
      cell: ({ row }) => `v${row.original.version}`,
    },
    { accessorKey: "source", header: "Source" },
    {
      accessorKey: "status",
      header: "Status",
      cell: ({ row }) => (
        <StatusBadge status={getDeploymentLifecycleStatus(row.original)} />
      ),
    },
    {
      accessorKey: "build_status",
      header: "Build",
      cell: ({ row }) => <StatusBadge status={row.original.build_status} />,
    },
    {
      accessorKey: "created_at",
      header: "Created",
      cell: ({ row }) => formatDate(row.original.created_at),
    },
    {
      id: "actions",
      header: "",
      cell: ({ row }) => (
        <div className="flex items-center justify-end gap-2">
          <Link
            href={`${base}/sites/${siteId}/deployments/${row.original.id}`}
            className="text-xs text-cyan-300"
          >
            Inspect
          </Link>
          {canManage &&
          getDeploymentLifecycleStatus(row.original) === "ready" ? (
            <Button
              size="sm"
              variant="outline"
              disabled={activate.isPending}
              onClick={() =>
                activate.mutate(row.original.id, {
                  onSuccess: () => toast.success("Site deployment activated"),
                })
              }
            >
              Activate
            </Button>
          ) : null}
        </div>
      ),
    },
  ];
  return (
    <>
      <BackLink href={`${base}/sites`} label="Back to sites" />
      <PageHeader
        eyebrow="Site"
        title={site.name}
        description="Deploy immutable static site archives and inspect their build history."
        actions={
          canManage ? (
            <Button
              type="button"
              disabled={upload.isPending}
              onClick={openFilePicker}
            >
              <FileUp className="size-4" />
              {upload.isPending ? "Uploading…" : "Deploy site"}
            </Button>
          ) : deployments.data ? (
            <Badge variant="neutral">Read-only</Badge>
          ) : null
        }
      />
      <input
        ref={fileInputRef}
        type="file"
        accept=".zip,.tar,.gz,.tgz"
        className="sr-only"
        disabled={upload.isPending}
        aria-label="Select site deployment archive"
        onChange={(event) => {
          const file = event.target.files?.[0];
          event.currentTarget.value = "";
          if (file) handleDeploymentUpload(file);
        }}
      />
      <div className="mb-5 flex flex-wrap items-center gap-2">
        <StatusBadge status={site.status} />
        <span className="text-xs text-slate-600">
          {site.framework} · {formatBytes(site.artifact_used_bytes)} used
        </span>
        <span className="text-xs text-slate-600">
          Updated {formatDate(site.updated_at)}
        </span>
        <ResourceId id={site.id} label="Site ID" />
      </div>
      {showFirstDeployment ? (
        <Card className="mb-4 border-amber-300/20 bg-amber-300/[0.03]">
          <CardContent className="flex flex-col gap-2 p-4 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <p className="text-sm font-medium text-amber-100">
                Ready for first deployment
              </p>
              <p className="mt-1 text-xs leading-5 text-amber-200/70">
                Upload an archive to build and publish this site. Build logs
                will be available from the deployment detail.
              </p>
            </div>
            {canManage ? (
              <Button
                type="button"
                variant="outline"
                disabled={upload.isPending}
                onClick={openFilePicker}
              >
                <FileUp className="size-4" /> Deploy site
              </Button>
            ) : null}
          </CardContent>
        </Card>
      ) : null}
      <Card>
        <CardHeader>
          <CardTitle>Deployments</CardTitle>
        </CardHeader>
        <DataTable
          data={deployments.data?.deployments ?? []}
          columns={columns}
          loading={deployments.isLoading}
          empty="No site deployments yet."
          serverPagination={pageControls(
            deploymentsNavigation,
            nextCursor(deployments.data),
            deployments.isFetching,
          )}
        />
      </Card>
    </>
  );
}

export function SiteDeploymentView({
  organizationId,
  projectId,
  siteId,
  deploymentId,
}: {
  organizationId: string;
  projectId: string;
  siteId: string;
  deploymentId: string;
}) {
  const query = useSiteDeployment(projectId, siteId, deploymentId);
  const logFetcher = useCallback(
    async (after?: number): Promise<LogLine[]> => {
      const result = await api.GET(
        "/v1/projects/{projectID}/sites/{siteID}/deployments/{deploymentID}/logs",
        {
          params: {
            path: {
              projectID: projectId,
              siteID: siteId,
              deploymentID: deploymentId,
            },
            query: after === undefined ? { limit: 100 } : { limit: 100, after },
          },
        },
      );
      const data = await unwrap(result);
      return (data?.logs ?? []) as LogLine[];
    },
    [deploymentId, projectId, siteId],
  );
  const deployment = query.data?.deployment;
  if (query.error)
    return <ErrorState error={query.error} retry={() => query.refetch()} />;
  if (!deployment)
    return (
      <EmptyState
        title="Deployment not found"
        description="The deployment may have been removed or is outside this site."
      />
    );
  return (
    <>
      <BackLink
        href={`/organizations/${organizationId}/projects/${projectId}/sites/${siteId}`}
        label="Back to site"
      />
      <PageHeader
        eyebrow="Site deployment"
        title={`Version ${deployment.version}`}
        description="Immutable site deployment metadata and incremental build logs."
        actions={
          <StatusBadge status={getDeploymentLifecycleStatus(deployment)} />
        }
      />
      <div className="mb-5 flex flex-wrap items-center gap-2">
        <ResourceId id={deployment.id} label="Deployment ID" />
        <span className="text-xs text-slate-600">
          Updated {formatDate(deployment.updated_at)}
        </span>
        <span className="text-xs text-slate-600">
          Queued {formatDate(deployment.queued_at)}
        </span>
      </div>
      <div className="grid gap-4 md:grid-cols-4">
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-slate-500">Build</p>
            <div className="mt-2">
              <StatusBadge status={deployment.build_status} />
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-slate-500">Source</p>
            <p className="mt-2 text-sm text-white">{deployment.source}</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-slate-500">Archive</p>
            <p className="mt-2 text-sm text-white">
              {formatBytes(deployment.archive_size_bytes)}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs text-slate-500">Duration</p>
            <p className="mt-2 text-sm text-white">
              {formatDuration(deploymentDurationMs(deployment))}
            </p>
            {deployment.build_started_at ? (
              <p className="mt-1 text-xs text-slate-600">
                Started {formatDate(deployment.build_started_at)}
              </p>
            ) : null}
          </CardContent>
        </Card>
      </div>
      {isDeploymentInProgress(deployment) ? (
        <Card className="mt-5 border-violet-300/20 bg-violet-300/[0.04]">
          <CardContent className="p-5">
            <p className="text-sm font-medium text-violet-100">
              Build in progress
            </p>
            <p className="mt-1 text-xs leading-5 text-violet-200/70">
              This deployment is queued or building. Its status and build logs
              refresh automatically.
            </p>
          </CardContent>
        </Card>
      ) : null}
      {deployment.error_message ||
      getDeploymentLifecycleStatus(deployment) === "failed" ? (
        <Card className="mt-5 border-rose-300/20 bg-rose-400/[0.04]">
          <CardContent className="space-y-3 p-5">
            <div>
              <p className="text-xs uppercase tracking-[0.14em] text-rose-300">
                Deployment failed
              </p>
              <p className="mt-2 whitespace-pre-wrap text-sm text-rose-100">
                {deployment.error_message ??
                  "The build did not complete successfully."}
              </p>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button asChild variant="outline">
                <a href="#build-logs">View build logs</a>
              </Button>
              <Button asChild variant="ghost">
                <Link
                  href={`/organizations/${organizationId}/projects/${projectId}/sites/${siteId}`}
                >
                  Deploy another archive
                </Link>
              </Button>
            </div>
          </CardContent>
        </Card>
      ) : null}
      <div id="build-logs" className="mt-5 scroll-mt-6">
        <LogViewer
          key={deploymentId}
          title="Build logs"
          description="Backend sequence cursor; only new lines are requested while following."
          fetchPage={logFetcher}
          emptyMessage="No logs yet. Build output will appear when this deployment starts."
        />
      </div>
    </>
  );
}
