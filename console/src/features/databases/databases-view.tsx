"use client";
import Link from "next/link";
import { type ColumnDef } from "@tanstack/react-table";
import { Database as DatabaseIcon, MoreHorizontal } from "lucide-react";
import { useRouter } from "next/navigation";
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
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { ProjectResourceIntro } from "@/features/resources/collection-shared";
import { pageControls } from "@/lib/pagination";
import { ResourceId } from "@/components/resource-id";
import { databaseName } from "./data-values";

export function DatabasesView({
  organizationId,
  projectId,
}: {
  organizationId: string;
  projectId: string;
}) {
  const router = useRouter();
  const navigation = useCursorPagination();
  const [createOpen, setCreateOpen] = useState(false);
  const query = useDatabases(projectId, { cursor: navigation.cursor });
  const create = useCreateDatabase(projectId);
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const canManage = query.data?.can_manage === true;
  const handleCreateDatabase = async (values: Record<string, string>) => {
    const result = await create.mutateAsync({
      name: databaseName.parse(values.name),
    });
    toast.success("Database created");
    if (result?.database.id) {
      router.push(`${base}/databases/${result.database.id}`);
    }
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
        <ResourceId id={row.original.id} label="Database ID" />
      ),
    },
    {
      accessorKey: "created_at",
      header: "Created",
      cell: ({ row }) => formatDate(row.original.created_at),
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
          canManage ? (
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
          ) : query.data ? (
            <Badge variant="neutral">Read-only</Badge>
          ) : null
        }
      />
      <ProjectResourceIntro
        icon={DatabaseIcon}
        title="Typed data layer"
        description="Organize structured application data into tables, inspect rows, and manage backups."
      />
      {query.isError ? (
        <ErrorState
          title="Could not load databases"
          error={query.error}
          retry={() => query.refetch()}
        />
      ) : query.data?.databases.length ||
        navigation.canFirst ||
        nextCursor(query.data) ? (
        <Card>
          <DataTable
            data={query.data?.databases ?? []}
            columns={columns}
            loading={query.isLoading}
            empty="No databases on this page."
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
          description={
            canManage
              ? "Create a database to define tables and browse rows through the platform API."
              : "No databases are available to manage in this project."
          }
          actionLabel={canManage ? "Create database" : undefined}
          action={canManage ? () => setCreateOpen(true) : undefined}
        />
      )}
    </>
  );
}
