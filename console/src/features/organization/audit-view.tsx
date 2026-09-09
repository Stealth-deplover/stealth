"use client";
import { nextCursor } from "@/api/pagination";
import { useOrganizationAudit } from "@/api/queries";
import { DataTable } from "@/components/data-table";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { Card } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { pageControls } from "@/lib/pagination";

export function OrganizationAuditView({
  organizationId,
}: {
  organizationId: string;
}) {
  const navigation = useCursorPagination();
  const query = useOrganizationAudit(organizationId, {
    cursor: navigation.cursor,
  });
  return (
    <>
      <PageHeader
        eyebrow="Organization"
        title="Audit"
        description="Durable activity events emitted by the platform."
      />
      {query.isError ? (
        <ErrorState
          title="Could not load organization audit events"
          error={query.error}
          retry={() => query.refetch()}
        />
      ) : (
        <Card>
          <DataTable
            data={query.data?.events ?? []}
            loading={query.isLoading}
            columns={[
              {
                accessorKey: "action",
                header: "Action",
                cell: ({ row }) => (
                  <div>
                    <p className="font-medium text-white">
                      {row.original.action}
                    </p>
                    <p className="text-xs text-slate-500">
                      {row.original.target_type}
                    </p>
                  </div>
                ),
              },
              {
                accessorKey: "actor_email",
                header: "Actor",
                cell: ({ row }) => row.original.actor_email ?? "System",
              },
              {
                accessorKey: "created_at",
                header: "When",
                cell: ({ row }) => formatDate(row.original.created_at),
              },
            ]}
            serverPagination={pageControls(
              navigation,
              nextCursor(query.data),
              query.isFetching,
            )}
          />
        </Card>
      )}
    </>
  );
}
