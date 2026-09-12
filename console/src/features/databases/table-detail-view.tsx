"use client";

import { useState, type FormEvent } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import type { ColumnDef } from "@tanstack/react-table";
import { toast } from "sonner";
import { ComponentsParametersOrderDirection } from "@/api/generated/schema";
import type { DatabaseColumn, DatabaseRow } from "@/api/types";
import {
  useDatabaseTable,
  useDatabaseTables,
  useDatabaseColumns,
  useDatabaseIndexes,
  useDatabaseRows,
  useDatabaseRow,
} from "@/api/queries";
import {
  useCreateDatabaseColumn,
  useCreateDatabaseRow,
  useUpdateDatabaseRow,
  useDeleteDatabaseRow,
} from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { CreateDialog } from "@/components/create-dialog";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DataTable } from "@/components/data-table";
import { EmptyState } from "@/components/empty-state";
import { ErrorState, errorMessage } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { pageControls } from "@/lib/pagination";
import { formatDate } from "@/lib/format";
import { BackLink } from "@/features/resources/detail-shared";
import { parseRowData } from "./data-values";
import { RowValue } from "./row-value";
import {
  databaseColumnFields,
  databaseColumnPayload,
  databasePartialRowPayload,
  databaseRowFields,
  databaseRowPayload,
  type DatabaseColumnFormValues,
  type DatabaseRowFormValues,
} from "@/features/databases/table-form";

type TableScope = { projectId: string; databaseId: string; tableId: string };
const selectClass =
  "h-9 rounded-lg border border-stealth-border bg-stealth-panel px-2 text-sm text-white";

function RowDetail({
  projectId,
  databaseId,
  tableId,
  rowId,
  columns,
  canManage,
  onClose,
}: TableScope & {
  rowId: string;
  columns: DatabaseColumn[];
  canManage: boolean;
  onClose: () => void;
}) {
  const query = useDatabaseRow(projectId, databaseId, tableId, rowId);
  const update = useUpdateDatabaseRow(projectId, databaseId, tableId);
  const remove = useDeleteDatabaseRow(projectId, databaseId, tableId);
  const row = query.data?.row;
  return (
    <Dialog
      open={Boolean(rowId)}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>Row detail</DialogTitle>
          <DialogDescription>
            Inspect field values and row metadata.
          </DialogDescription>
        </DialogHeader>
        {query.error ? (
          <ErrorState
            title="Could not load row"
            error={query.error}
            retry={() => query.refetch()}
          />
        ) : null}
        {query.isPending ? (
          <LoadingState />
        ) : row ? (
          <div className="space-y-5">
            <ResourceId id={row.id} label="Row ID" />
            <p className="text-xs text-slate-500">
              Created {formatDate(row.created_at)} · Updated{" "}
              {formatDate(row.updated_at)}
            </p>
            <dl className="divide-y divide-stealth-border">
              {Object.entries(row.data ?? {}).map(([key, value]) => (
                <div
                  key={key}
                  className="grid gap-2 py-3 sm:grid-cols-[10rem_1fr]"
                >
                  <dt className="break-all font-mono text-xs text-slate-500">
                    {key}
                  </dt>
                  <dd className="min-w-0">
                    <RowValue
                      value={value}
                      type={columns.find((column) => column.key === key)?.type}
                      expanded
                    />
                  </dd>
                </div>
              ))}
            </dl>
            {canManage ? (
              <div className="flex gap-2">
                <CreateDialog<DatabaseRowFormValues>
                  key={row.id + row.updated_at}
                  triggerLabel="Edit row"
                  submitLabel="Save row"
                  pendingLabel="Saving row…"
                  title="Edit row"
                  description="Supply typed JSON fields. Omitted fields keep their current values; null clears an optional value."
                  fields={databaseRowFields(JSON.stringify(row.data, null, 2))}
                  pending={update.isPending}
                  onSubmit={async (values) => {
                    await update.mutateAsync({
                      rowId: row.id,
                      body: databasePartialRowPayload(values, columns),
                    });
                    toast.success("Row updated");
                  }}
                />
                <ConfirmDialog
                  trigger={<Button variant="destructive">Delete row</Button>}
                  title="Delete this row?"
                  description={`Permanently remove row ${row.id}. References from other tables may prevent deletion.`}
                  confirmLabel="Delete row"
                  pending={remove.isPending}
                  onConfirm={async () => {
                    await remove.mutateAsync(row.id);
                    toast.success("Row deleted");
                    onClose();
                  }}
                />
              </div>
            ) : null}
          </div>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

export function DatabaseRowsView({
  organizationId,
  projectId,
  databaseId,
  tableId,
}: TableScope & { organizationId: string }) {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const table = useDatabaseTable(projectId, databaseId, tableId);
  const permissions = useDatabaseTables(projectId, databaseId);
  const columns = useDatabaseColumns(projectId, databaseId, tableId);
  const indexes = useDatabaseIndexes(projectId, databaseId, tableId);
  const navigation = useCursorPagination("rows_cursor");
  const rows = useDatabaseRows(projectId, databaseId, tableId, {
    cursor: navigation.cursor,
    filter: searchParams.get("filter") || undefined,
    order_by: searchParams.get("order_by") || undefined,
    order_direction:
      searchParams.get("order_direction") === "desc"
        ? ComponentsParametersOrderDirection.desc
        : ComponentsParametersOrderDirection.asc,
    search: searchParams.get("search") || undefined,
    search_column: searchParams.get("search_column") || undefined,
  });
  const createColumn = useCreateDatabaseColumn(projectId, databaseId, tableId);
  const createRow = useCreateDatabaseRow(projectId, databaseId, tableId);
  const [addOpen, setAddOpen] = useState(false);
  const canManage = permissions.data?.can_manage === true;
  const schema = columns.data ?? [];
  const updateUrl = (
    updates: Record<string, string | undefined>,
    resetCursor = false,
  ) => {
    const next = new URLSearchParams(searchParams.toString());
    for (const [key, value] of Object.entries(updates)) {
      if (value) next.set(key, value);
      else next.delete(key);
    }
    if (resetCursor) next.delete("rows_cursor");
    router.replace(`${pathname}${next.size ? `?${next}` : ""}`, {
      scroll: false,
    });
  };
  const indexedKeys = new Set(
    (indexes.data ?? [])
      .filter((index) => index.type === "key" || index.type === "unique")
      .map((index) => index.column_keys[0]),
  );
  const searchableKeys = (indexes.data ?? [])
    .filter((index) => index.type === "fulltext")
    .map((index) => index.column_keys[0]);
  const applyQuery = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const filter = String(form.get("filter") ?? "").trim();
    const search = String(form.get("search") ?? "").trim();
    const searchColumn = String(form.get("search_column") ?? "");
    try {
      if (filter) {
        if (filter.length > 4096)
          throw new Error("Filters must be at most 4096 characters.");
        const data = parseRowData(filter, schema, true);
        if (Object.keys(data).some((key) => !indexedKeys.has(key)))
          throw new Error("Each filtered column needs a key or unique index.");
      }
      if (search && (!searchColumn || !searchableKeys.includes(searchColumn)))
        throw new Error("Choose a full-text indexed column for search.");
      updateUrl(
        {
          filter: filter || undefined,
          search: search || undefined,
          search_column: search ? searchColumn : undefined,
          order_by: String(form.get("order_by") ?? "id"),
          order_direction: String(form.get("order_direction") ?? "asc"),
        },
        true,
      );
    } catch (error) {
      toast.error(errorMessage(error));
    }
  };
  if (table.isPending) return <LoadingState />;
  if (table.error && !table.data)
    return (
      <ErrorState
        title="Could not load table"
        error={table.error}
        retry={() => table.refetch()}
      />
    );
  const current = table.data?.table;
  if (!current)
    return (
      <EmptyState
        title="Table not found"
        description="This table may have been removed."
      />
    );
  const rowColumns: ColumnDef<DatabaseRow, unknown>[] = [
    {
      accessorKey: "id",
      header: "Row ID",
      cell: ({ row }) => (
        <Button
          variant="ghost"
          size="sm"
          onClick={() => updateUrl({ row_id: row.original.id })}
        >
          Inspect {row.original.id.slice(0, 8)}…
        </Button>
      ),
    },
    ...Array.from(
      new Set([
        ...schema.map((column) => column.key),
        ...(rows.data?.rows ?? []).flatMap((row) =>
          Object.keys(row.data ?? {}),
        ),
      ]),
    ).map((key): ColumnDef<DatabaseRow, unknown> => ({
      id: `data:${key}`,
      header: key,
      cell: ({ row }) => (
        <RowValue
          value={row.original.data?.[key]}
          type={schema.find((column) => column.key === key)?.type}
        />
      ),
    })),
    {
      accessorKey: "updated_at",
      header: "Updated",
      cell: ({ row }) => formatDate(row.original.updated_at),
    },
  ];
  return (
    <>
      <BackLink
        href={`/organizations/${organizationId}/projects/${projectId}/databases/${databaseId}`}
        label="Back to database"
      />
      <PageHeader
        eyebrow="Database table"
        title={current.name}
        description="Inspect schema and browse typed application data."
        actions={
          canManage ? (
            <CreateDialog<DatabaseRowFormValues>
              open={addOpen}
              onOpenChange={setAddOpen}
              triggerLabel="Add row"
              submitLabel="Add row"
              pendingLabel="Adding row…"
              title="Add row"
              description="Use declared column names and JSON values. Application permissions remain denied by default."
              fields={databaseRowFields()}
              disabled={columns.isPending || Boolean(columns.error)}
              pending={createRow.isPending}
              onSubmit={async (values) => {
                const result = await createRow.mutateAsync(
                  databaseRowPayload(values, schema),
                );
                toast.success("Row added");
                if (result?.row.id) updateUrl({ row_id: result.row.id });
              }}
            />
          ) : undefined
        }
      />
      <div className="mb-5 flex flex-wrap items-center gap-3 text-xs text-slate-500">
        <ResourceId id={current.id} label="Table ID" />
        <Badge variant="neutral">
          Row security {current.row_security ? "enabled" : "disabled"}
        </Badge>
        <span>Created {formatDate(current.created_at)}</span>
        <span>Updated {formatDate(current.updated_at)}</span>
      </div>
      {table.error ? (
        <ErrorState
          title="Could not refresh table"
          error={table.error}
          retry={() => table.refetch()}
        />
      ) : null}
      {permissions.error ? (
        <ErrorState
          title="Could not load table access"
          error={permissions.error}
          retry={() => permissions.refetch()}
        />
      ) : null}
      <Tabs defaultValue="rows">
        <TabsList>
          <TabsTrigger value="rows">Rows</TabsTrigger>
          <TabsTrigger value="schema">Schema</TabsTrigger>
        </TabsList>
        <TabsContent value="rows">
          {columns.error ? (
            <ErrorState
              title="Could not load table schema"
              error={columns.error}
              retry={() => columns.refetch()}
            />
          ) : null}
          {indexes.error ? (
            <ErrorState
              title="Could not load query indexes"
              error={indexes.error}
              retry={() => indexes.refetch()}
            />
          ) : null}
          <Card>
            <form
              key={searchParams.toString()}
              onSubmit={applyQuery}
              className="flex flex-wrap items-end gap-3 border-b border-stealth-border p-4"
            >
              <label className="grid gap-1 text-xs text-slate-500">
                Equality filters (JSON)
                <Input
                  name="filter"
                  aria-label="Equality filters (JSON)"
                  defaultValue={searchParams.get("filter") ?? ""}
                  placeholder='{"email":"ada@example.com"}'
                  maxLength={4096}
                  disabled={!indexedKeys.size}
                />
              </label>
              <label className="grid gap-1 text-xs text-slate-500">
                Order by
                <select
                  name="order_by"
                  className={selectClass}
                  defaultValue={searchParams.get("order_by") ?? "id"}
                >
                  <option value="id">Row ID</option>
                  {schema
                    .filter(
                      (column) =>
                        column.required &&
                        indexedKeys.has(column.key) &&
                        column.type !== "json",
                    )
                    .map((column) => (
                      <option key={column.key} value={column.key}>
                        {column.key}
                      </option>
                    ))}
                </select>
              </label>
              <label className="grid gap-1 text-xs text-slate-500">
                Direction
                <select
                  name="order_direction"
                  className={selectClass}
                  defaultValue={searchParams.get("order_direction") ?? "asc"}
                >
                  <option value="asc">Ascending</option>
                  <option value="desc">Descending</option>
                </select>
              </label>
              {searchableKeys.length ? (
                <>
                  <label className="grid gap-1 text-xs text-slate-500">
                    Search phrase
                    <Input
                      name="search"
                      defaultValue={searchParams.get("search") ?? ""}
                      maxLength={256}
                    />
                  </label>
                  <label className="grid gap-1 text-xs text-slate-500">
                    Search column
                    <select
                      name="search_column"
                      className={selectClass}
                      defaultValue={
                        searchParams.get("search_column") ?? searchableKeys[0]
                      }
                    >
                      {searchableKeys.map((key) => (
                        <option key={key}>{key}</option>
                      ))}
                    </select>
                  </label>
                </>
              ) : null}
              <Button
                type="submit"
                variant="outline"
                disabled={
                  columns.isPending ||
                  indexes.isPending ||
                  Boolean(columns.error || indexes.error)
                }
              >
                Apply
              </Button>
              <Button
                type="button"
                variant="ghost"
                onClick={() =>
                  updateUrl(
                    {
                      filter: undefined,
                      order_by: undefined,
                      order_direction: undefined,
                      search: undefined,
                      search_column: undefined,
                    },
                    true,
                  )
                }
              >
                Clear
              </Button>
              <p className="w-full text-xs text-slate-500">
                Filters and ordering apply on the server. Column filters require
                indexes; full-text search requires a full-text index.
              </p>
            </form>
            {rows.error ? (
              <ErrorState
                title="Could not load table rows"
                error={rows.error}
                retry={() => rows.refetch()}
              />
            ) : null}
            {!rows.isPending &&
            !rows.error &&
            !rows.data?.rows.length &&
            !nextCursor(rows.data) &&
            !navigation.canFirst ? (
              <EmptyState
                title={
                  searchParams.get("filter") || searchParams.get("search")
                    ? "No matching rows"
                    : "No rows yet"
                }
                description="Rows will appear here after data is inserted."
                actionLabel={
                  canManage && !columns.error && !columns.isPending
                    ? "Add row"
                    : undefined
                }
                action={
                  canManage && !columns.error && !columns.isPending
                    ? () => setAddOpen(true)
                    : undefined
                }
              />
            ) : (
              <DataTable
                data={rows.data?.rows ?? []}
                columns={rowColumns}
                loading={rows.isPending}
                empty="No rows on this page."
                serverPagination={pageControls(
                  navigation,
                  nextCursor(rows.data),
                  rows.isFetching,
                )}
              />
            )}
          </Card>
        </TabsContent>
        <TabsContent value="schema">
          <Card>
            <CardHeader className="flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <CardTitle>Columns</CardTitle>
              {canManage ? (
                <CreateDialog<DatabaseColumnFormValues>
                  triggerLabel="Create column"
                  submitLabel="Create column"
                  pendingLabel="Creating column…"
                  title="Create column"
                  description="Required columns need a default when rows already exist. Column types cannot be edited after creation."
                  fields={databaseColumnFields}
                  pending={createColumn.isPending}
                  onSubmit={async (values) => {
                    await createColumn.mutateAsync(
                      databaseColumnPayload(values),
                    );
                    toast.success("Column created");
                  }}
                />
              ) : null}
            </CardHeader>
            <CardContent>
              {columns.error ? (
                <ErrorState
                  title="Could not load table schema"
                  error={columns.error}
                  retry={() => columns.refetch()}
                />
              ) : null}
              <DataTable
                data={schema}
                loading={columns.isPending}
                empty="No columns yet. Create a column to define the table schema."
                columns={[
                  { accessorKey: "key", header: "Column" },
                  {
                    accessorKey: "type",
                    header: "Type",
                    cell: ({ row }) => (
                      <span className="font-mono text-xs">
                        {row.original.type}
                        {row.original.type === "varchar"
                          ? `(${row.original.varchar_size})`
                          : ""}
                      </span>
                    ),
                  },
                  {
                    id: "nullable",
                    header: "Nullable",
                    cell: ({ row }) => (row.original.required ? "No" : "Yes"),
                  },
                  {
                    id: "default",
                    header: "Default",
                    cell: ({ row }) => (
                      <RowValue
                        value={row.original.default}
                        type={row.original.type}
                      />
                    ),
                  },
                ]}
              />
              <p className="mt-4 text-xs text-slate-500">
                Row ID and created/updated timestamps are managed automatically
                and are separate from user columns.
              </p>
            </CardContent>
          </Card>
          <Card className="mt-4">
            <CardHeader>
              <CardTitle>Indexes</CardTitle>
            </CardHeader>
            <CardContent>
              {indexes.error ? (
                <ErrorState
                  title="Could not load indexes"
                  error={indexes.error}
                  retry={() => indexes.refetch()}
                />
              ) : null}
              <DataTable
                data={indexes.data ?? []}
                loading={indexes.isPending}
                empty="No indexes. Row ID ordering remains available."
                columns={[
                  { accessorKey: "name", header: "Index" },
                  { accessorKey: "type", header: "Type" },
                  {
                    id: "keys",
                    header: "Columns",
                    cell: ({ row }) => row.original.column_keys.join(", "),
                  },
                ]}
              />
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
      {searchParams.get("row_id") ? (
        <RowDetail
          projectId={projectId}
          databaseId={databaseId}
          tableId={tableId}
          rowId={searchParams.get("row_id")!}
          columns={schema}
          canManage={canManage && !columns.error && !columns.isPending}
          onClose={() => updateUrl({ row_id: undefined })}
        />
      ) : null}
    </>
  );
}
