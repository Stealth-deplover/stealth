"use client";

import { toast } from "sonner";
import type { DatabaseColumn } from "@/api/types";
import { useDatabaseRow } from "@/api/queries";
import { useDeleteDatabaseRow, useUpdateDatabaseRow } from "@/api/mutations";
import { CreateDialog } from "@/components/create-dialog";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { ResourceId } from "@/components/resource-id";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { formatDate } from "@/lib/format";
import {
  databasePartialRowPayload,
  databaseRowFields,
  type DatabaseRowFormValues,
} from "@/features/databases/table-form";
import { RowValue } from "./row-value";
import type { TableScope } from "./table-scope";

export function TableRowDetail({
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
