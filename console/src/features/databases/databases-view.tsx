"use client";
import Link from "next/link";
import { type ColumnDef } from "@tanstack/react-table";
import { Database as DatabaseIcon, MoreHorizontal } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { useCreateDatabase } from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { useDatabases } from "@/api/queries";
import type { Database } from "@/api/types";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { ProjectResourceIntro } from "@/features/resources/collection-shared";
import { pageControls } from "@/lib/pagination";

export function DatabasesView({
  organizationId,
  projectId,
}: {
  organizationId: string;
  projectId: string;
}) {
  const navigation = useCursorPagination();
  const [createOpen, setCreateOpen] = useState(false);
  const query = useDatabases(projectId, { cursor: navigation.cursor });
  const create = useCreateDatabase(projectId);
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const handleCreateDatabase = async (values: Record<string, string>) => {
    await create.mutateAsync({ name: values.name });
    toast.success("Database created");
  };
  const columns: ColumnDef<Database, unknown>[] = [
    {
      accessorKey: "name",
      header: "Database",
      cell: ({ row }) => (
        <Link
          href={`${base}/databases/${row.original.id}`}
          className="font-medium text-white hover:text-amber-200"
        >
          {row.original.name}
        </Link>
      ),
    },
    {
      accessorKey: "id",
      header: "Database ID",
      cell: ({ row }) => (
        <span className="font-mono text-xs text-slate-500">
          {row.original.id.slice(0, 8)}…
        </span>
      ),
    },
    {
      accessorKey: "updated_at",
      header: "Updated",
      cell: ({ row }) => formatDate(row.original.updated_at),
    },
    {
      id: "open",
      header: "",
      cell: ({ row }) => (
        <Button asChild size="sm" variant="ghost">
          <Link href={`${base}/databases/${row.original.id}`}>
            Browse <MoreHorizontal className="size-3.5" />
          </Link>
        </Button>
      ),
    },
  ];
  return (
    <>
      <PageHeader
        eyebrow="Data"
        title="Databases"
        description="Browse typed schemas, rows, indexes, relationships, and backups."
        actions={
          <CreateDialog
            open={createOpen}
            onOpenChange={setCreateOpen}
            triggerLabel="Create database"
            submitLabel="Create database"
            pendingLabel="Creating database…"
            title="Create a database"
            description="A database gives your project a typed schema boundary."
            fields={[{ name: "name", label: "Name", placeholder: "primary" }]}
            pending={create.isPending}
            onSubmit={handleCreateDatabase}
          />
        }
      />
      <ProjectResourceIntro
        icon={DatabaseIcon}
        title="Typed data layer"
        description="The API exposes schema and row operations. Query execution is intentionally not presented because the backend does not provide it."
      />
      {query.isError ? (
        <ErrorState error={query.error} retry={() => query.refetch()} />
      ) : query.data?.databases.length ? (
        <Card>
          <DataTable
            data={query.data.databases}
            columns={columns}
            serverPagination={pageControls(
              navigation,
              nextCursor(query.data),
              query.isFetching,
            )}
          />
        </Card>
      ) : query.isLoading ? (
        <Card>
          <DataTable data={[]} columns={columns} loading />
        </Card>
      ) : (
        <EmptyState
          title="No databases yet"
          description="Create a database to define tables and browse rows through the platform API."
          actionLabel="Create database"
          action={() => setCreateOpen(true)}
        />
      )}
    </>
  );
}
