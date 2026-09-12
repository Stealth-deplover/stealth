"use client";
import Link from "next/link";
import { type ColumnDef } from "@tanstack/react-table";
import { Globe2 } from "lucide-react";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";
import { useCreateSite } from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { useSites } from "@/api/queries";
import type { Site } from "@/api/types";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { ResourceTableCard } from "@/components/resource-page";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { formatBytes, formatDate } from "@/lib/format";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { ProjectResourceIntro } from "@/features/resources/collection-shared";
import { pageControls } from "@/lib/pagination";
import {
  siteFields,
  sitePayload,
  type SiteFormValues,
} from "@/features/sites/site-form";

export function SitesView({
  organizationId,
  projectId,
}: {
  organizationId: string;
  projectId: string;
}) {
  const router = useRouter();
  const navigation = useCursorPagination();
  const [createOpen, setCreateOpen] = useState(false);
  const query = useSites(projectId, { cursor: navigation.cursor });
  const create = useCreateSite(projectId);
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const canManage = query.data?.can_manage === true;
  const handleCreateSite = async (values: SiteFormValues) => {
    const result = await create.mutateAsync(sitePayload(values));
    toast.success("Site created");
    if (result?.site) {
      router.push(`${base}/sites/${result.site.id}`);
    }
  };
  const columns: ColumnDef<Site, unknown>[] = [
    {
      accessorKey: "name",
      header: "Site",
      cell: ({ row }) => (
        <Link
          href={`${base}/sites/${row.original.id}`}
          className="font-medium text-white hover:text-violet-200"
        >
          <span className="block">{row.original.name}</span>
          <span className="mt-0.5 block text-[11px] text-slate-600">
            {row.original.framework}
          </span>
        </Link>
      ),
    },
    {
      accessorKey: "status",
      header: "Status",
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: "active_deployment_id",
      header: "Deployment",
      cell: ({ row }) =>
        row.original.active_deployment_id ? (
          <StatusBadge status="active" />
        ) : (
          <span className="text-xs text-slate-600">Not deployed</span>
        ),
    },
    {
      accessorKey: "artifact_used_bytes",
      header: "Artifact",
      cell: ({ row }) =>
        `${formatBytes(row.original.artifact_used_bytes)} / ${formatBytes(row.original.artifact_quota_bytes)}`,
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
        title="Sites"
        description="Deploy static sites from an archive or a public GitHub/GitLab source."
        actions={
          canManage ? (
            <CreateDialog<SiteFormValues>
              open={createOpen}
              onOpenChange={setCreateOpen}
              triggerLabel="Create site"
              submitLabel="Create site"
              pendingLabel="Creating site…"
              title="Create a site"
              description="Create the site boundary first, then add a deployment."
              fields={siteFields}
              pending={create.isPending}
              onSubmit={handleCreateSite}
            />
          ) : query.data ? (
            <Badge variant="neutral">Read-only</Badge>
          ) : null
        }
      />
      <ProjectResourceIntro
        icon={Globe2}
        title="Sites"
        description="Site deployments are immutable and asynchronous. Deployment state is read from the actual site endpoints."
      />
      {query.isError ? (
        <ErrorState
          title="Could not load sites"
          error={query.error}
          retry={() => query.refetch()}
        />
      ) : query.data?.sites.length ? (
        <ResourceTableCard
          data={query.data.sites}
          searchable={(item, term) =>
            `${item.name} ${item.status}`.toLowerCase().includes(term)
          }
          serverPagination={pageControls(
            navigation,
            nextCursor(query.data),
            query.isFetching,
          )}
        >
          {(filtered, pagination) => (
            <DataTable
              data={filtered}
              columns={columns}
              serverPagination={pagination}
            />
          )}
        </ResourceTableCard>
      ) : query.isLoading ? (
        <Card>
          <DataTable data={[]} columns={columns} loading />
        </Card>
      ) : (
        <EmptyState
          title="No sites yet"
          description={
            canManage
              ? "Sites publish static applications and documentation. Create one to deploy your first archive."
              : "No sites are available to manage in this project."
          }
          actionLabel={canManage ? "Create site" : undefined}
          action={canManage ? () => setCreateOpen(true) : undefined}
        />
      )}
    </>
  );
}
