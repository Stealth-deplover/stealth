"use client";

import { type FormEvent } from "react";
import { useSearchParams } from "next/navigation";
import type { ColumnDef } from "@tanstack/react-table";
import { toast } from "sonner";
import { ComponentsParametersOrderDirection } from "@/api/generated/schema";
import type { DatabaseColumn, DatabaseIndex, DatabaseRow } from "@/api/types";
import { useDatabaseRows } from "@/api/queries";
import { useCreateDatabaseRow } from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { EmptyState } from "@/components/empty-state";
import { ErrorState, errorMessage } from "@/components/feedback/error-state";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { pageControls } from "@/lib/pagination";
import { formatDate } from "@/lib/format";
import {
  databaseRowFields,
  databaseRowPayload,
  type DatabaseRowFormValues,
} from "@/features/databases/table-form";
import { parseRowData } from "./data-values";
import { RowValue } from "./row-value";
import type { TableScope, TableURLUpdate } from "./table-scope";

const selectClass =
  "h-9 rounded-lg border border-stealth-border bg-stealth-panel px-2 text-sm text-white";

type TableRowsSharedProps = TableScope & {
  schema: DatabaseColumn[];
  indexes: DatabaseIndex[];
  columnsPending: boolean;
  columnsError: unknown;
  retryColumns: () => void;
  indexesPending: boolean;
  indexesError: unknown;
  retryIndexes: () => void;
  canManage: boolean;
  updateUrl: TableURLUpdate;
};

export function TableRowCreateDialog({
  projectId,
  databaseId,
  tableId,
  schema,
  columnsPending,
  columnsError,
  canManage,
  open,
  onOpenChange,
  onRowCreated,
}: TableScope & {
  schema: DatabaseColumn[];
  columnsPending: boolean;
  columnsError: unknown;
  canManage: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onRowCreated: (rowId: string) => void;
}) {
  const createRow = useCreateDatabaseRow(projectId, databaseId, tableId);
  if (!canManage) return null;
  return (
    <CreateDialog<DatabaseRowFormValues>
      open={open}
      onOpenChange={onOpenChange}
      triggerLabel="Add row"
      submitLabel="Add row"
      pendingLabel="Adding row…"
      title="Add row"
      description="Use declared column names and JSON values. Application permissions remain denied by default."
      fields={databaseRowFields()}
      disabled={columnsPending || Boolean(columnsError)}
      pending={createRow.isPending}
      onSubmit={async (values) => {
        const result = await createRow.mutateAsync(
          databaseRowPayload(values, schema),
        );
        if (result?.row.id) onRowCreated(result.row.id);
      }}
    />
  );
}

export function TableRowsPanel({
  projectId,
  databaseId,
  tableId,
  schema,
  indexes,
  columnsPending,
  columnsError,
  retryColumns,
  indexesPending,
  indexesError,
  retryIndexes,
  canManage,
  updateUrl,
  onAddRow,
}: TableRowsSharedProps & { onAddRow: () => void }) {
  const searchParams = useSearchParams();
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
  const indexedKeys = new Set(
    indexes
      .filter((index) => index.type === "key" || index.type === "unique")
      .map((index) => index.column_keys[0]),
  );
  const searchableKeys = indexes
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
      {columnsError ? (
        <ErrorState
          title="Could not load table schema"
          error={columnsError}
          retry={retryColumns}
        />
      ) : null}
      {indexesError ? (
        <ErrorState
          title="Could not load query indexes"
          error={indexesError}
          retry={retryIndexes}
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
              columnsPending ||
              indexesPending ||
              Boolean(columnsError || indexesError)
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
              canManage && !columnsError && !columnsPending
                ? "Add row"
                : undefined
            }
            action={
              canManage && !columnsError && !columnsPending
                ? onAddRow
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
    </>
  );
}
