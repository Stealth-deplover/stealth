"use client";
import { nextCursor } from "@/api/pagination";
import { useOrganizationTraces } from "@/api/queries";
import { DataTable } from "@/components/data-table";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { StatusBadge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
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
        description="Durable root HTTP request traces. The backend does not currently expose span hierarchy or server-side status filters."
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
                cell: ({ row }) => `${row.original.duration_ms} ms`,
              },
              {
                accessorKey: "trace_id",
                header: "Trace ID",
                cell: ({ row }) => (
                  <span className="font-mono text-xs text-slate-500">
                    {row.original.trace_id}
                  </span>
                ),
              },
            ]}
            loading={query.isLoading}
            empty="No traces returned."
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
