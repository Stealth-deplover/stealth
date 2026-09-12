"use client";

import type { ColumnDef } from "@tanstack/react-table";
import { useState } from "react";
import { toast } from "sonner";
import { nextCursor } from "@/api/pagination";
import {
  useCreateFunctionVariable,
  useDeleteFunctionVariable,
} from "@/api/mutations";
import { useFunctionVariables } from "@/api/queries";
import type { FunctionVariable } from "@/api/types";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { ErrorState } from "@/components/feedback/error-state";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { formatDate } from "@/lib/format";
import { pageControls } from "@/lib/pagination";
import {
  functionVariableFields,
  functionVariablePayload,
  type FunctionVariableFormValues,
} from "@/features/functions/function-variable-form";

// This module owns the complete variable-configuration workflow: query,
// pagination, typed form intake, mutation feedback, and table actions.
export function FunctionVariablesPanel({
  projectId,
  functionId,
}: {
  projectId: string;
  functionId: string;
}) {
  const navigation = useCursorPagination("variables_cursor");
  const query = useFunctionVariables(projectId, functionId, {
    cursor: navigation.cursor,
  });
  const create = useCreateFunctionVariable(projectId, functionId);
  const remove = useDeleteFunctionVariable(projectId, functionId);
  const canManage = query.data?.can_manage === true;
  const [deletingVariableId, setDeletingVariableId] = useState<string | null>(
    null,
  );
  const handleCreateVariable = async (values: FunctionVariableFormValues) => {
    await create.mutateAsync(functionVariablePayload(values));
    toast.success("Environment variable added");
  };
  const columns: ColumnDef<FunctionVariable, unknown>[] = [
    {
      accessorKey: "key",
      header: "Key",
      cell: ({ row }) => (
        <span className="font-mono text-xs text-white">{row.original.key}</span>
      ),
    },
    {
      accessorKey: "kind",
      header: "Kind",
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
        <span className="text-xs text-slate-400">
          {row.original.has_value ? "Configured · hidden" : "Not set"}
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
          <ConfirmDialog
            trigger={
              <Button variant="ghost" size="sm" className="text-rose-300">
                Delete
              </Button>
            }
            title="Delete this variable?"
            description={`The value for ${row.original.key} will be removed from the function configuration.`}
            confirmLabel="Delete variable"
            pending={remove.isPending && deletingVariableId === row.original.id}
            onConfirm={async () => {
              setDeletingVariableId(row.original.id);
              try {
                await remove.mutateAsync(row.original.id);
                toast.success("Variable deleted");
              } finally {
                setDeletingVariableId(null);
              }
            }}
          />
        ) : null,
    },
  ];
  if (query.error)
    return <ErrorState error={query.error} retry={() => query.refetch()} />;
  return (
    <Card className="mt-4">
      <CardHeader className="flex-row items-center justify-between">
        <div>
          <CardTitle>Environment variables</CardTitle>
          <p className="mt-1 text-xs text-slate-500">
            Values are write-only. The Go API returns metadata and never sends
            plaintext values back to this browser.
          </p>
        </div>
        {canManage ? (
          <CreateDialog<FunctionVariableFormValues>
            triggerLabel="Add variable"
            submitLabel="Add variable"
            pendingLabel="Adding variable…"
            title="Add environment variable"
            description="The value is encrypted by the Go API and cannot be recovered after submission."
            fields={functionVariableFields}
            pending={create.isPending}
            onSubmit={handleCreateVariable}
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
          empty="No environment variables configured."
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
