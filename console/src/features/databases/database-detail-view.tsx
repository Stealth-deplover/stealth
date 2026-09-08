"use client";
import Link from "next/link";
import type { ColumnDef } from "@tanstack/react-table";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";
import { apiUrl } from "@/api/client";
import { nextCursor } from "@/api/pagination";
import {
  useCreateDatabaseBackup,
  useCreateDatabaseTable,
  useDeleteDatabaseBackup,
  useRestoreDatabaseBackup,
} from "@/api/mutations";
import {
  useDatabase,
  useDatabaseBackups,
  useDatabaseTables,
} from "@/api/queries";
import type { DatabaseBackup, DatabaseTable } from "@/api/types";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { ErrorState, errorMessage } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { formatBytes, formatDate } from "@/lib/format";
import { pageControls } from "@/lib/pagination";
import { BackLink } from "@/features/resources/detail-shared";
import { databaseName } from "./data-values";

export { DatabaseRowsView } from "./table-detail-view";

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
  const [created, setCreated] = useState<DatabaseBackup>();
  const columns: ColumnDef<DatabaseBackup, unknown>[] = [
    {
      accessorKey: "id",
      header: "Backup ID",
      cell: ({ row }) => <ResourceId id={row.original.id} label="Backup ID" />,
    },
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
      header: "SHA-256",
      cell: ({ row }) => (
        <ResourceId id={row.original.checksum_sha256} label="Backup checksum" />
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
          {query.data?.can_manage === true ? (
            <>
              <ConfirmDialog
                trigger={
                  <Button variant="ghost" size="sm">
                    Restore
                  </Button>
                }
                title="Restore this backup?"
                description={`Backup ${row.original.id} replaces all current tables, rows, columns, indexes, and relationships in this database. Any validation failure cancels the entire restore.`}
                confirmLabel="Restore backup"
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
                description={`Permanently delete backup ${row.original.id}. Current database data is not changed.`}
                confirmLabel="Delete backup"
                pending={remove.isPending}
                onConfirm={async () => {
                  await remove.mutateAsync(row.original.id);
                  if (created?.id === row.original.id) setCreated(undefined);
                  toast.success("Backup deleted");
                }}
              />
            </>
          ) : (
            <span className="text-xs text-slate-600">Read-only</span>
          )}
        </div>
      ),
    },
  ];
  const handleCreate = () =>
    create.mutate(undefined, {
      onSuccess: (result) => {
        setCreated(result?.backup);
        toast.success("Backup created");
      },
      onError: (error) => toast.error(errorMessage(error)),
    });
  return (
    <Card>
      <CardHeader className="flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <CardTitle>Backups</CardTitle>
          <p className="mt-1 text-xs text-slate-500">
            Complete logical snapshots, up to 10,000 rows and 50 MB. Larger
            databases cannot be backed up through this operation.
          </p>
        </div>
        {query.data?.can_manage === true ? (
          <Button disabled={create.isPending} onClick={handleCreate}>
            {create.isPending ? "Creating backup…" : "Create backup"}
          </Button>
        ) : query.data?.can_manage === false ? (
          <Badge variant="neutral">Read-only</Badge>
        ) : null}
      </CardHeader>
      <CardContent className="space-y-4">
        {create.error ? (
          <ErrorState
            title="Could not create backup"
            error={create.error}
            retry={handleCreate}
          />
        ) : null}
        {created ? (
          <div
            role="status"
            className="flex flex-wrap items-center gap-2 text-sm text-emerald-200"
          >
            Backup created <ResourceId id={created.id} label="Backup ID" />{" "}
            {formatBytes(created.size_bytes)}
          </div>
        ) : null}
        {query.error ? (
          <ErrorState
            title="Could not load database backups"
            error={query.error}
            retry={() => query.refetch()}
          />
        ) : null}
        {!query.isPending &&
        !query.error &&
        !query.data?.backups.length &&
        !nextCursor(query.data) &&
        !navigation.canFirst ? (
          <EmptyState
            title="No backups yet"
            description="Create a backup to capture the current database state."
          />
        ) : (
          <DataTable
            data={query.data?.backups ?? []}
            columns={columns}
            loading={query.isPending}
            empty="No backups on this page."
            serverPagination={pageControls(
              navigation,
              nextCursor(query.data),
              query.isFetching,
            )}
          />
        )}
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
  const router = useRouter();
  const query = useDatabase(projectId, databaseId);
  const tablesNavigation = useCursorPagination("tables_cursor");
  const tables = useDatabaseTables(projectId, databaseId, {
    cursor: tablesNavigation.cursor,
  });
  const backups = useDatabaseBackups(projectId, databaseId);
  const [createTableOpen, setCreateTableOpen] = useState(false);
  const createTable = useCreateDatabaseTable(projectId, databaseId);
  const database = query.data?.database;
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const handleCreateTable = async (values: Record<string, string>) => {
    const result = await createTable.mutateAsync({
      name: databaseName.parse(values.name),
      row_security: values.row_security !== "false",
    });
    toast.success("Table created");
    if (result?.table.id)
      router.push(`${base}/databases/${databaseId}/tables/${result.table.id}`);
    else tablesNavigation.goFirst();
  };
  if (query.error && !database)
    return (
      <ErrorState
        title="Could not load database"
        error={query.error}
        retry={() => query.refetch()}
      />
    );
  if (query.isPending) return <LoadingState />;
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
        <Badge variant="neutral">
          {row.original.row_security ? "Enabled" : "Disabled"}
        </Badge>
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
          <Link
            href={`${base}/databases/${databaseId}/tables/${row.original.id}`}
          >
            Open table
          </Link>
        </Button>
      ),
    },
  ];
  const tableData = tables.data?.tables ?? [];
  const tableCount =
    tables.data &&
    !tables.isPlaceholderData &&
    !tablesNavigation.cursor &&
    !nextCursor(tables.data)
      ? tableData.length
      : undefined;
  const latestBackup =
    backups.data && !nextCursor(backups.data)
      ? [...backups.data.backups].sort(
          (first, second) =>
            Date.parse(second.created_at) - Date.parse(first.created_at),
        )[0]
      : undefined;
  return (
    <>
      <BackLink href={`${base}/databases`} label="Back to databases" />
      <PageHeader
        eyebrow="Database"
        title={database.name}
        description="Manage tables, inspect application data, and capture logical backups."
        actions={
          <CreateDialog
            open={createTableOpen}
            onOpenChange={setCreateTableOpen}
            triggerLabel="Create table"
            submitLabel="Create table"
            pendingLabel="Creating table…"
            title="Create a table"
            description="Define a table before adding columns and rows. Application permissions start denied."
            fields={[
              {
                name: "name",
                label: "Table name",
                placeholder: "users",
                help: "Use 2–120 characters.",
              },
              {
                name: "row_security",
                label: "Row security",
                type: "select",
                defaultValue: "true",
                options: [
                  { value: "true", label: "Enabled" },
                  { value: "false", label: "Disabled" },
                ],
                help: "When enabled, individual row grants can allow access in addition to table grants.",
              },
            ]}
            onSubmit={handleCreateTable}
            pending={createTable.isPending}
            disabled={tables.data?.can_manage === false}
          />
        }
      />
      <div className="mb-5 flex flex-wrap items-center gap-3 text-xs text-slate-500">
        <ResourceId id={database.id} label="Database ID" />
        <span>Created {formatDate(database.created_at)}</span>
        <span>Updated {formatDate(database.updated_at)}</span>
      </div>
      {query.error ? (
        <ErrorState
          title="Could not refresh database"
          error={query.error}
          retry={() => query.refetch()}
        />
      ) : null}
      <Tabs defaultValue="overview">
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="tables">Tables</TabsTrigger>
          <TabsTrigger value="backups">Backups</TabsTrigger>
          <TabsTrigger value="settings">Settings</TabsTrigger>
        </TabsList>
        <TabsContent value="overview">
          <Card>
            <CardHeader>
              <CardTitle>Database overview</CardTitle>
            </CardHeader>
            <CardContent>
              <dl className="grid gap-5 sm:grid-cols-2">
                <div>
                  <dt className="text-xs text-slate-500">Name</dt>
                  <dd className="mt-1">{database.name}</dd>
                </div>
                {tableCount !== undefined ? (
                  <div>
                    <dt className="text-xs text-slate-500">Tables</dt>
                    <dd className="mt-1">{tableCount}</dd>
                  </div>
                ) : null}
                {latestBackup ? (
                  <div>
                    <dt className="text-xs text-slate-500">Latest backup</dt>
                    <dd className="mt-1">
                      <ResourceId id={latestBackup.id} label="Backup ID" />{" "}
                      {formatDate(latestBackup.created_at)}
                    </dd>
                  </div>
                ) : null}
              </dl>
              {!tables.isPending &&
              !tables.error &&
              tableData.length === 0 &&
              !nextCursor(tables.data) &&
              !tablesNavigation.canFirst ? (
                <div className="mt-5">
                  <EmptyState
                    title="No tables yet"
                    description="Create a table to start storing structured application data."
                    actionLabel={
                      tables.data?.can_manage === false
                        ? undefined
                        : "Create table"
                    }
                    action={() => setCreateTableOpen(true)}
                  />
                </div>
              ) : null}
              {tables.error ? (
                <ErrorState
                  title="Could not load database tables"
                  error={tables.error}
                  retry={() => tables.refetch()}
                />
              ) : null}
              {backups.error ? (
                <ErrorState
                  title="Could not load database backups"
                  error={backups.error}
                  retry={() => backups.refetch()}
                />
              ) : null}
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent value="tables">
          <Card>
            <CardHeader>
              <CardTitle>Tables</CardTitle>
            </CardHeader>
            <CardContent>
              {tables.error ? (
                <ErrorState
                  title="Could not load database tables"
                  error={tables.error}
                  retry={() => tables.refetch()}
                />
              ) : null}
              {!tables.isPending &&
              !tables.error &&
              tableData.length === 0 &&
              !nextCursor(tables.data) &&
              !tablesNavigation.canFirst ? (
                <EmptyState
                  title="No tables yet"
                  description="Create a table to start storing structured application data."
                  actionLabel={
                    tables.data?.can_manage === false
                      ? undefined
                      : "Create table"
                  }
                  action={() => setCreateTableOpen(true)}
                />
              ) : (
                <DataTable
                  data={tableData}
                  columns={columns}
                  loading={tables.isPending}
                  empty="No tables on this page."
                  serverPagination={pageControls(
                    tablesNavigation,
                    nextCursor(tables.data),
                    tables.isFetching,
                  )}
                />
              )}
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent value="backups">
          <DatabaseBackupsPanel projectId={projectId} databaseId={databaseId} />
        </TabsContent>
        <TabsContent value="settings">
          <Card>
            <CardHeader>
              <CardTitle>Database settings</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <p className="text-sm text-slate-400">Name: {database.name}</p>
              <ResourceId id={database.id} label="Database ID" />
              <p className="text-sm text-slate-500">
                Database names are read-only here. Configure columns and row
                access in each table.
              </p>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </>
  );
}
