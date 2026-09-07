"use client";
import Link from "next/link";
import type { ColumnDef } from "@tanstack/react-table";
import { useRouter, useSearchParams } from "next/navigation";
import { Table2 } from "lucide-react";
import { toast } from "sonner";
import { apiUrl } from "@/api/client";
import { nextCursor } from "@/api/pagination";
import {
  useCreateDatabaseBackup,
  useDeleteDatabaseBackup,
  useRestoreDatabaseBackup,
} from "@/api/mutations";
import {
  useDatabase,
  useDatabaseBackups,
  useDatabaseRows,
  useDatabaseTables,
} from "@/api/queries";
import type { DatabaseBackup, DatabaseRow, DatabaseTable } from "@/api/types";
import { DataTable } from "@/components/data-table";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { formatBytes, formatDate } from "@/lib/format";
import { Input } from "@/components/ui/input";
import { pageControls } from "@/lib/pagination";
import { BackLink } from "@/features/resources/detail-shared";

function DatabaseBackupsPanel({
  projectId,
  databaseId,
}: {
  projectId: string;
  databaseId: string;
}) {
  const navigation = useCursorPagination("backups_cursor");
  const query = useDatabaseBackups(projectId, databaseId, {
    cursor: navigation.cursor,
  });
  const create = useCreateDatabaseBackup(projectId, databaseId);
  const remove = useDeleteDatabaseBackup(projectId, databaseId);
  const restore = useRestoreDatabaseBackup(projectId, databaseId);
  const columns: ColumnDef<DatabaseBackup, unknown>[] = [
    {
      accessorKey: "created_at",
      header: "Created",
      cell: ({ row }) => formatDate(row.original.created_at),
    },
    {
      accessorKey: "size_bytes",
      header: "Size",
      cell: ({ row }) => formatBytes(row.original.size_bytes),
    },
    {
      accessorKey: "checksum_sha256",
      header: "Checksum",
      cell: ({ row }) => (
        <span className="font-mono text-[11px] text-slate-500">
          {row.original.checksum_sha256.slice(0, 16)}…
        </span>
      ),
    },
    {
      id: "actions",
      header: "",
      cell: ({ row }) => (
        <div className="flex items-center justify-end gap-1">
          <Button asChild variant="ghost" size="sm">
            <a
              href={apiUrl(
                `/v1/projects/${projectId}/databases/${databaseId}/backups/${row.original.id}/download`,
              )}
            >
              Download
            </a>
          </Button>
          <ConfirmDialog
            trigger={
              <Button variant="ghost" size="sm">
                Restore
              </Button>
            }
            title="Restore this backup?"
            description="Restoring replaces the database schema, rows, indexes, and relationships. The Go API applies the operation atomically."
            confirmLabel="Restore database"
            pending={restore.isPending}
            onConfirm={async () => {
              await restore.mutateAsync(row.original.id);
              toast.success("Database restored");
            }}
          />
          <ConfirmDialog
            trigger={
              <Button variant="ghost" size="sm" className="text-rose-300">
                Delete
              </Button>
            }
            title="Delete this backup?"
            description="The immutable backup blob and metadata will be removed."
            confirmLabel="Delete backup"
            pending={remove.isPending}
            onConfirm={async () => {
              await remove.mutateAsync(row.original.id);
              toast.success("Backup deleted");
            }}
          />
        </div>
      ),
    },
  ];
  if (query.error)
    return <ErrorState error={query.error} retry={() => query.refetch()} />;
  return (
    <Card className="mt-5">
      <CardHeader className="flex-row items-center justify-between">
        <div>
          <CardTitle>Backups</CardTitle>
          <p className="mt-1 text-xs text-slate-500">
            Immutable logical snapshots of schema, rows, indexes, and
            relationships.
          </p>
        </div>
        <Button
          variant="outline"
          disabled={create.isPending}
          onClick={() =>
            create.mutate(undefined, {
              onSuccess: () => toast.success("Backup created"),
              onError: (error) =>
                toast.error(
                  error instanceof Error
                    ? error.message
                    : "Unable to create backup",
                ),
            })
          }
        >
          {create.isPending ? "Creating…" : "Create backup"}
        </Button>
      </CardHeader>
      <CardContent>
        <DataTable
          data={query.data?.backups ?? []}
          columns={columns}
          loading={query.isLoading}
          empty="No database backups yet."
          serverPagination={pageControls(
            navigation,
            nextCursor(query.data),
            query.isFetching,
          )}
        />
      </CardContent>
    </Card>
  );
}

export function DatabaseDetailView({
  organizationId,
  projectId,
  databaseId,
}: {
  organizationId: string;
  projectId: string;
  databaseId: string;
}) {
  const query = useDatabase(projectId, databaseId);
  const tablesNavigation = useCursorPagination("tables_cursor");
  const tables = useDatabaseTables(projectId, databaseId, {
    cursor: tablesNavigation.cursor,
  });
  const database = query.data?.database;
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  if (query.error)
    return <ErrorState error={query.error} retry={() => query.refetch()} />;
  if (tables.error)
    return <ErrorState error={tables.error} retry={() => tables.refetch()} />;
  if (!database)
    return (
      <EmptyState
        title="Database not found"
        description="The database may have been removed or is outside this project."
      />
    );
  const columns: ColumnDef<DatabaseTable, unknown>[] = [
    {
      accessorKey: "name",
      header: "Table",
      cell: ({ row }) => (
        <Link
          href={`${base}/databases/${databaseId}/tables/${row.original.id}`}
          className="font-medium text-white hover:text-amber-200"
        >
          {row.original.name}
        </Link>
      ),
    },
    {
      accessorKey: "row_security",
      header: "Row security",
      cell: ({ row }) => (
        <StatusBadge
          status={row.original.row_security ? "active" : "inactive"}
        />
      ),
    },
    {
      accessorKey: "updated_at",
      header: "Updated",
      cell: ({ row }) => formatDate(row.original.updated_at),
    },
  ];
  return (
    <>
      <BackLink href={`${base}/databases`} label="Back to databases" />
      <PageHeader
        eyebrow="Database"
        title={database.name}
        description="Schema browsing, row operations, and immutable backups backed by the database API."
      />
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Table2 className="size-4 text-amber-300" /> Tables
          </CardTitle>
        </CardHeader>
        <DataTable
          data={tables.data?.tables ?? []}
          columns={columns}
          loading={tables.isLoading}
          empty="No tables yet."
          serverPagination={pageControls(
            tablesNavigation,
            nextCursor(tables.data),
            tables.isFetching,
          )}
        />
      </Card>
      <DatabaseBackupsPanel projectId={projectId} databaseId={databaseId} />
    </>
  );
}

export function DatabaseRowsView({
  organizationId,
  projectId,
  databaseId,
  tableId,
}: {
  organizationId: string;
  projectId: string;
  databaseId: string;
  tableId: string;
}) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const rowsNavigation = useCursorPagination("rows_cursor");
  const rows = useDatabaseRows(projectId, databaseId, tableId, {
    cursor: rowsNavigation.cursor,
    search: searchParams.get("search") ?? undefined,
    search_column: searchParams.get("search_column") ?? undefined,
  });
  const data = rows.data?.rows ?? [];
  const keys = Array.from(
    new Set(data.flatMap((row) => Object.keys(row.data))),
  );
  const updateQuery = (updates: Record<string, string | undefined>) => {
    const next = new URLSearchParams(searchParams.toString());
    Object.entries(updates).forEach(([key, value]) =>
      value ? next.set(key, value) : next.delete(key),
    );
    router.replace(
      `${window.location.pathname}${next.toString() ? `?${next.toString()}` : ""}`,
      { scroll: false },
    );
  };
  return (
    <>
      <BackLink
        href={`/organizations/${organizationId}/projects/${projectId}/databases/${databaseId}`}
        label="Back to database"
      />
      <PageHeader
        eyebrow="Rows"
        title="Table data"
        description="Typed rows returned by the backend. Search uses the declared indexed column contract."
      />
      <Card>
        <div className="flex flex-wrap items-end gap-3 border-b border-stealth-border p-4">
          <div className="min-w-52 flex-1">
            <label htmlFor="row-search" className="text-xs text-slate-500">
              Search phrase
            </label>
            <Input
              id="row-search"
              className="mt-1"
              value={searchParams.get("search") ?? ""}
              onChange={(event) =>
                updateQuery({
                  search: event.target.value || undefined,
                  rows_cursor: undefined,
                })
              }
              placeholder="Search indexed text"
            />
          </div>
          <div className="min-w-44">
            <label
              htmlFor="row-search-column"
              className="text-xs text-slate-500"
            >
              Search column
            </label>
            <Input
              id="row-search-column"
              className="mt-1"
              value={searchParams.get("search_column") ?? ""}
              onChange={(event) =>
                updateQuery({
                  search_column: event.target.value || undefined,
                  rows_cursor: undefined,
                })
              }
              placeholder="title"
            />
          </div>
          <p className="w-full font-mono text-[11px] text-slate-600">
            table {tableId}
          </p>
        </div>
        <DataTable
          data={data}
          columns={[
            {
              accessorKey: "id",
              header: "ID",
              cell: ({ row }) => (
                <span className="font-mono text-xs text-slate-500">
                  {row.original.id.slice(0, 12)}…
                </span>
              ),
            },
            ...keys.map((key): ColumnDef<DatabaseRow, unknown> => ({
              id: key,
              header: key,
              cell: ({ row }) => (
                <span className="max-w-xs truncate text-xs text-slate-300">
                  {String(row.original.data[key] ?? "—")}
                </span>
              ),
            })),
            {
              accessorKey: "updated_at",
              header: "Updated",
              cell: ({ row }) => formatDate(row.original.updated_at),
            },
          ]}
          loading={rows.isLoading}
          empty="No rows returned."
          serverPagination={pageControls(
            rowsNavigation,
            nextCursor(rows.data),
            rows.isFetching,
          )}
        />
      </Card>
    </>
  );
}
