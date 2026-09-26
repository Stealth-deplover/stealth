"use client";

import { useState } from "react";
import { toast } from "sonner";
import {
  useCreateAppEnvironmentVariable,
  useDeleteAppEnvironmentVariable,
  useUpdateAppEnvironmentVariable,
} from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { useAppEnvironmentVariables } from "@/api/queries";
import type { AppEnvironmentVariable } from "@/api/types";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { CreateDialog, type CreateField } from "@/components/create-dialog";
import { DataTable, type DataTableColumnDef } from "@/components/data-table";
import { ErrorState } from "@/components/feedback/error-state";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { formatDate } from "@/lib/format";
import { pageControls } from "@/lib/pagination";

type CreateValues = {
  key: string;
  value: string;
  kind: string;
  description: string;
};
type ReplaceValue = { value: string };

const createFields: readonly CreateField<CreateValues>[] = [
  { name: "key", label: "Key", placeholder: "DATABASE_URL" },
  {
    name: "value",
    label: "Value",
    type: "password",
    autoComplete: "new-password",
  },
  {
    name: "kind",
    label: "Type",
    type: "select",
    defaultValue: "variable",
    options: [
      { value: "variable", label: "Variable" },
      { value: "secret", label: "Secret" },
    ],
  },
  { name: "description", label: "Description", required: false },
];

const replaceFields: readonly CreateField<ReplaceValue>[] = [
  {
    name: "value",
    label: "New value",
    type: "password",
    autoComplete: "new-password",
    required: false,
  },
];

export function AppEnvironmentVariablesPanel({
  projectId,
  appId,
}: {
  projectId: string;
  appId: string;
}) {
  const navigation = useCursorPagination("app_variables_cursor");
  const query = useAppEnvironmentVariables(projectId, appId, {
    cursor: navigation.cursor,
  });
  const create = useCreateAppEnvironmentVariable(projectId, appId);
  const update = useUpdateAppEnvironmentVariable(projectId, appId);
  const remove = useDeleteAppEnvironmentVariable(projectId, appId);
  const canManage = query.data?.can_manage === true;
  const [deletingId, setDeletingId] = useState<string | null>(null);

  const columns: DataTableColumnDef<AppEnvironmentVariable>[] = [
    {
      accessorKey: "key",
      header: "Key",
      cell: ({ row }) => (
        <span className="font-mono text-xs text-white">{row.original.key}</span>
      ),
    },
    {
      accessorKey: "is_secret",
      header: "Type",
      cell: ({ row }) => (
        <Badge variant={row.original.is_secret ? "warning" : "neutral"}>
          {row.original.is_secret ? "Secret" : "Variable"}
        </Badge>
      ),
    },
    {
      accessorKey: "has_value",
      header: "Value",
      cell: ({ row }) => (
        <span className="text-xs text-fog">
          {row.original.has_value ? "Configured · hidden" : "Not set"}
        </span>
      ),
    },
    {
      accessorKey: "description",
      header: "Description",
      cell: ({ row }) => (
        <span className="text-xs text-fog">
          {row.original.description || "—"}
        </span>
      ),
    },
    {
      accessorKey: "updated_at",
      header: "Updated",
      cell: ({ row }) => formatDate(row.original.updated_at),
    },
    {
      id: "actions",
      header: "",
      cell: ({ row }) =>
        canManage ? (
          <div className="flex flex-wrap items-center gap-2">
            <ReplaceVariableValue
              variable={row.original}
              pending={update.isPending}
              onSubmit={async (value) => {
                await update.mutateAsync({
                  variableId: row.original.id,
                  body: { value },
                });
                toast.success("App environment value replaced");
              }}
            />
            <ConfirmDialog
              trigger={
                <Button variant="ghost" size="sm" className="text-rose-300">
                  Delete
                </Button>
              }
              title={`Delete ${row.original.key}?`}
              description="This removes the value and metadata from the App. A configured value change replaces the current runtime."
              confirmLabel="Delete variable"
              pending={remove.isPending && deletingId === row.original.id}
              onConfirm={async () => {
                setDeletingId(row.original.id);
                try {
                  await remove.mutateAsync(row.original.id);
                  toast.success("App environment variable deleted");
                } finally {
                  setDeletingId(null);
                }
              }}
            />
          </div>
        ) : null,
    },
  ];

  if (query.error)
    return <ErrorState error={query.error} retry={() => query.refetch()} />;

  return (
    <Card className="mt-5">
      <CardHeader className="flex-row flex-wrap items-center justify-between gap-3">
        <div>
          <CardTitle>Environment variables</CardTitle>
          <p className="mt-1 max-w-2xl text-xs leading-5 text-fog">
            Values are encrypted before storage and write-only. Changing a
            configured value advances the desired generation and replaces the
            App runtime.
          </p>
        </div>
        {canManage ? (
          <CreateDialog<CreateValues>
            triggerLabel="Add variable"
            submitLabel="Add variable"
            pendingLabel="Adding variable…"
            title="Add App environment variable"
            description="The value is encrypted by the API and cannot be revealed after submission. Runtime values are not sent to the build worker."
            fields={createFields}
            pending={create.isPending}
            onSubmit={async (values) => {
              const description = values.description.trim();
              await create.mutateAsync({
                key: values.key.trim(),
                value: values.value,
                is_secret: values.kind === "secret",
                ...(description ? { description } : {}),
              });
              toast.success("App environment variable added");
            }}
          />
        ) : query.data ? (
          <Badge variant="neutral">Read-only</Badge>
        ) : null}
      </CardHeader>
      <CardContent>
        <DataTable
          data={query.data?.variables ?? []}
          columns={columns}
          loading={query.isLoading}
          empty="No App environment variables configured."
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

function ReplaceVariableValue({
  variable,
  pending,
  onSubmit,
}: {
  variable: AppEnvironmentVariable;
  pending: boolean;
  onSubmit: (value: string) => Promise<void>;
}) {
  return (
    <CreateDialog<ReplaceValue>
      triggerLabel="Replace value"
      submitLabel="Replace value"
      pendingLabel="Saving value…"
      title={`Replace ${variable.key}`}
      description="Submit a new value. The existing value cannot be displayed, and this App will restart after the desired generation advances."
      fields={replaceFields}
      pending={pending}
      trigger={
        <Button variant="outline" size="sm">
          Replace value
        </Button>
      }
      onSubmit={async ({ value }) => onSubmit(value)}
    />
  );
}
