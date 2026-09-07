"use client";
import type { ColumnDef } from "@tanstack/react-table";
import { nextCursor } from "@/api/pagination";
import { useMemberships } from "@/api/queries";
import type { Membership } from "@/api/types";
import { DataTable } from "@/components/data-table";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { pageControls } from "@/lib/pagination";

export function OrganizationMembersView({
  organizationId,
}: {
  organizationId: string;
}) {
  const navigation = useCursorPagination();
  const query = useMemberships(organizationId, { cursor: navigation.cursor });
  const columns: ColumnDef<Membership, unknown>[] = [
    {
      accessorKey: "email",
      header: "Identity",
      cell: ({ row }) => (
        <div>
          <p className="font-medium text-white">{row.original.email}</p>
          <p className="font-mono text-[10px] text-slate-600">
            {row.original.account_id}
          </p>
        </div>
      ),
    },
    {
      accessorKey: "role",
      header: "Role",
      cell: ({ row }) => (
        <Badge variant={row.original.role === "owner" ? "default" : "neutral"}>
          {row.original.role}
        </Badge>
      ),
    },
    {
      accessorKey: "created_at",
      header: "Joined",
      cell: ({ row }) => formatDate(row.original.created_at),
    },
  ];
  return (
    <>
      <PageHeader
        eyebrow="Organization"
        title="Members"
        description="Membership and roles are enforced by the Go API."
      />
      {query.isError ? (
        <ErrorState error={query.error} retry={() => query.refetch()} />
      ) : (
        <Card>
          <DataTable
            columns={columns}
            data={query.data?.memberships ?? []}
            loading={query.isLoading}
            empty="No memberships returned."
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
