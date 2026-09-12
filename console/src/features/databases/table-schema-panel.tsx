"use client";

import type { DatabaseColumn, DatabaseIndex } from "@/api/types";
import { useCreateDatabaseColumn } from "@/api/mutations";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { ErrorState } from "@/components/feedback/error-state";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { toast } from "sonner";
import {
  databaseColumnFields,
  databaseColumnPayload,
  type DatabaseColumnFormValues,
} from "@/features/databases/table-form";
import { RowValue } from "./row-value";
import type { TableScope } from "./table-scope";

type TableSchemaPanelProps = TableScope & {
  schema: DatabaseColumn[];
  indexes: DatabaseIndex[];
  columnsPending: boolean;
  columnsError: unknown;
  retryColumns: () => void;
  indexesPending: boolean;
  indexesError: unknown;
  retryIndexes: () => void;
  canManage: boolean;
};

export function TableSchemaPanel({
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
}: TableSchemaPanelProps) {
  const createColumn = useCreateDatabaseColumn(projectId, databaseId, tableId);

  return (
    <>
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
                await createColumn.mutateAsync(databaseColumnPayload(values));
                toast.success("Column created");
              }}
            />
          ) : null}
        </CardHeader>
        <CardContent>
          {columnsError ? (
            <ErrorState
              title="Could not load table schema"
              error={columnsError}
              retry={retryColumns}
            />
          ) : null}
          <DataTable
            data={schema}
            loading={columnsPending}
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
            Row ID and created/updated timestamps are managed automatically and
            are separate from user columns.
          </p>
        </CardContent>
      </Card>
      <Card className="mt-4">
        <CardHeader>
          <CardTitle>Indexes</CardTitle>
        </CardHeader>
        <CardContent>
          {indexesError ? (
            <ErrorState
              title="Could not load indexes"
              error={indexesError}
              retry={retryIndexes}
            />
          ) : null}
          <DataTable
            data={indexes}
            loading={indexesPending}
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
    </>
  );
}
