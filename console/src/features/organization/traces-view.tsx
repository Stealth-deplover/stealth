"use client";
import { nextCursor } from "@/api/pagination";
import { useOrganizationTraces } from "@/api/queries";
import { DataTable } from "@/components/data-table";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { StatusBadge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { formatDate, formatDuration } from "@/lib/format";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { pageControls } from "@/lib/pagination";

export function OrganizationTracesView({
  organizationId,
}: {
  organizationId: string;
}) {
  const navigation = useCursorPagination();
  const query = useOrganizationTraces(organizationId, {
    cursor: navigation.cursor,
  });
  return (
    <>
      <PageHeader
        eyebrow="Organization"
        title="Traces"
        description="Inspect durable root HTTP request traces from across the organization."
      />
      {query.isError ? (
        <ErrorState error={query.error} retry={() => query.refetch()} />
      ) : (
        <Card>
          <DataTable
            data={query.data?.traces ?? []}
            columns={[
              {
                accessorKey: "started_at",
                header: "Timestamp",
                cell: ({ row }) => formatDate(row.original.started_at),
              },
              { accessorKey: "service", header: "Service" },
              {
                accessorKey: "method",
                header: "Method",
                cell: ({ row }) => (
                  <span className="font-mono text-xs text-cyan-200">
                    {row.original.method}
                  </span>
                ),
              },
              {
                accessorKey: "status",
                header: "Status",
                cell: ({ row }) => (
                  <StatusBadge
                    status={
                      row.original.status >= 500
                        ? "error"
                        : row.original.status >= 400
                          ? "warning"
                          : "success"
                    }
                  />
                ),
              },
              {
                accessorKey: "duration_ms",
                header: "Duration",
                cell: ({ row }) => formatDuration(row.original.duration_ms),
              },
              {
                accessorKey: "trace_id",
                header: "Trace ID",
                cell: ({ row }) => (
                  <ResourceId id={row.original.trace_id} label="Trace ID" />
                ),
              },
            ]}
            loading={query.isLoading}
            empty="No traces recorded for this organization."
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
