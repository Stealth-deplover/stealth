"use client";
import { nextCursor } from "@/api/pagination";
import { useProjectTraces } from "@/api/queries";
import { DataTable } from "@/components/data-table";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { HttpStatusBadge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { formatDate, formatDuration } from "@/lib/format";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";

export function TracesView({ projectId }: { projectId: string }) {
  const navigation = useCursorPagination();
  const query = useProjectTraces(projectId, { cursor: navigation.cursor });
  if (query.error)
    return <ErrorState error={query.error} retry={() => query.refetch()} />;
  return (
    <>
      <PageHeader
        eyebrow="Observability"
        title="Traces"
        description="Inspect backend-owned root HTTP requests and their timing."
      />
      <Card>
        <DataTable
          data={query.data?.traces ?? []}
          loading={query.isLoading}
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
              accessorKey: "route",
              header: "Route",
              cell: ({ row }) => (
                <span className="font-mono text-xs text-slate-400">
                  {row.original.route}
                </span>
              ),
            },
            {
              accessorKey: "status",
              header: "Status",
              cell: ({ row }) => (
                <HttpStatusBadge status={row.original.status} />
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
          empty="No traces recorded for this project."
          serverPagination={{
            canFirst: navigation.canFirst,
            canPrevious: navigation.canPrevious,
            canNext: Boolean(nextCursor(query.data)),
            onFirst: navigation.goFirst,
            onPrevious: navigation.goPrevious,
            onNext: () => navigation.goNext(nextCursor(query.data)),
            isFetching: query.isFetching,
          }}
        />
      </Card>
    </>
  );
}
