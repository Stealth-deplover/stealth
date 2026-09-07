"use client";
import Link from "next/link";
import type { ColumnDef } from "@tanstack/react-table";
import { FileUp } from "lucide-react";
import { useCallback } from "react";
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
import { StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { formatBytes, formatDate } from "@/lib/format";
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
  const deploymentsNavigation = useCursorPagination("site_deployments_cursor");
  const deployments = useSiteDeployments(projectId, siteId, {
    cursor: deploymentsNavigation.cursor,
  });
  const upload = useUploadSiteDeployment(projectId, siteId);
  const activate = useActivateSiteDeployment(projectId, siteId);
  const site = query.data?.site;
  const base = `/organizations/${organizationId}/projects/${projectId}`;
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
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
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
          {row.original.status === "ready" ? (
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
        description="Static site deployment boundary."
        actions={
          <label className="inline-flex h-9 cursor-pointer items-center gap-2 rounded-lg bg-cyan-300 px-3.5 text-sm font-medium text-slate-950 hover:bg-cyan-200">
            <FileUp className="size-4" /> Upload archive
            <input
              type="file"
              accept=".zip,.tar,.gz,.tgz"
              className="sr-only"
              onChange={(event) => {
                const file = event.target.files?.[0];
                if (file)
                  upload.mutate(
                    { file },
                    {
                      onSuccess: () => toast.success("Site deployment queued"),
                    },
                  );
              }}
            />
          </label>
        }
      />
      <div className="mb-5 flex items-center gap-2">
        <StatusBadge status={site.status} />
        <span className="text-xs text-slate-600">
          {site.framework} · {formatBytes(site.artifact_used_bytes)} used
        </span>
      </div>
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
        actions={<StatusBadge status={deployment.status} />}
      />
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
            <p className="text-xs text-slate-500">Queued</p>
            <p className="mt-2 text-sm text-white">
              {formatDate(deployment.queued_at)}
            </p>
          </CardContent>
        </Card>
      </div>
      {deployment.error_message ? (
        <Card className="mt-5 border-rose-300/20 bg-rose-400/[0.04]">
          <CardContent className="p-5">
            <p className="text-xs uppercase tracking-[0.14em] text-rose-300">
              Build error
            </p>
            <p className="mt-2 whitespace-pre-wrap text-sm text-rose-100">
              {deployment.error_message}
            </p>
          </CardContent>
        </Card>
      ) : null}
      <div className="mt-5">
        <LogViewer
          title="Build logs"
          description="Backend sequence cursor; only new lines are requested while following."
          fetchPage={logFetcher}
        />
      </div>
    </>
  );
}
