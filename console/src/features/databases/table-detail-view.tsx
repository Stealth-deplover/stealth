"use client";

import { useState } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import {
  useDatabaseColumns,
  useDatabaseIndexes,
  useDatabaseTable,
  useDatabaseTables,
} from "@/api/queries";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { formatDate } from "@/lib/format";
import { BackLink } from "@/features/resources/detail-shared";
import { TableRowCreateDialog, TableRowsPanel } from "./table-rows-panel";
import { TableRowDetail } from "./table-row-detail";
import { TableSchemaPanel } from "./table-schema-panel";
import type { TableScope, TableURLUpdate } from "./table-scope";

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
  const [addOpen, setAddOpen] = useState(false);
  const canManage = permissions.data?.can_manage === true;
  const schema = columns.data ?? [];
  const indexList = indexes.data ?? [];

  const updateUrl: TableURLUpdate = (updates, resetCursor = false) => {
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
          <TableRowCreateDialog
            projectId={projectId}
            databaseId={databaseId}
            tableId={tableId}
            schema={schema}
            columnsPending={columns.isPending}
            columnsError={columns.error}
            canManage={canManage}
            open={addOpen}
            onOpenChange={setAddOpen}
            onRowCreated={(rowId) => {
              setAddOpen(false);
              updateUrl({ row_id: rowId });
            }}
          />
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
          <TableRowsPanel
            projectId={projectId}
            databaseId={databaseId}
            tableId={tableId}
            schema={schema}
            indexes={indexList}
            columnsPending={columns.isPending}
            columnsError={columns.error}
            retryColumns={() => {
              void columns.refetch();
            }}
            indexesPending={indexes.isPending}
            indexesError={indexes.error}
            retryIndexes={() => {
              void indexes.refetch();
            }}
            canManage={canManage}
            updateUrl={updateUrl}
            onAddRow={() => setAddOpen(true)}
          />
        </TabsContent>
        <TabsContent value="schema">
          <TableSchemaPanel
            projectId={projectId}
            databaseId={databaseId}
            tableId={tableId}
            schema={schema}
            indexes={indexList}
            columnsPending={columns.isPending}
            columnsError={columns.error}
            retryColumns={() => {
              void columns.refetch();
            }}
            indexesPending={indexes.isPending}
            indexesError={indexes.error}
            retryIndexes={() => {
              void indexes.refetch();
            }}
            canManage={canManage}
          />
        </TabsContent>
      </Tabs>
      {searchParams.get("row_id") ? (
        <TableRowDetail
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
