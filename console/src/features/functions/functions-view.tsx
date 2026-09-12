"use client";
import Link from "next/link";
import { type ColumnDef } from "@tanstack/react-table";
import { FunctionSquare } from "lucide-react";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";
import { useCreateFunction } from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { useFunctions } from "@/api/queries";
import type { StealthFunction } from "@/api/types";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { ResourceTableCard } from "@/components/resource-page";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { ProjectResourceIntro } from "@/features/resources/collection-shared";
import { pageControls } from "@/lib/pagination";
import {
  functionFields,
  functionPayload,
  type FunctionFormValues,
} from "@/features/functions/function-form";

export function FunctionsView({
  organizationId,
  projectId,
}: {
  organizationId: string;
  projectId: string;
}) {
  const router = useRouter();
  const navigation = useCursorPagination();
  const [createOpen, setCreateOpen] = useState(false);
  const query = useFunctions(projectId, { cursor: navigation.cursor });
  const create = useCreateFunction(projectId);
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const canManage = query.data?.can_manage === true;
  const handleCreateFunction = async (values: FunctionFormValues) => {
    const result = await create.mutateAsync(functionPayload(values));
    toast.success("Function created");
    if (result?.function) {
      router.push(`${base}/functions/${result.function.id}`);
    }
  };
  const columns: ColumnDef<StealthFunction, unknown>[] = [
    {
      accessorKey: "name",
      header: "Function",
      cell: ({ row }) => (
        <Link
          href={`${base}/functions/${row.original.id}`}
          className="font-medium text-white hover:text-cyan-200"
        >
          <span className="block">{row.original.name}</span>
          <span className="mt-0.5 block text-[11px] text-slate-600">
            {row.original.entrypoint}
          </span>
        </Link>
      ),
    },
    {
      accessorKey: "runtime",
      header: "Runtime",
      cell: ({ row }) => (
        <span className="font-mono text-xs text-slate-400">
          {row.original.runtime}
        </span>
      ),
    },
    {
      accessorKey: "status",
      header: "Status",
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: "active_deployment_id",
      header: "Deployment",
      cell: ({ row }) =>
        row.original.active_deployment_id ? (
          <StatusBadge status="active" />
        ) : (
          <span className="text-xs text-slate-600">Not deployed</span>
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
      <PageHeader
        eyebrow="Compute"
        title="Functions"
        description="Run backend code on Stealth-managed infrastructure."
        actions={
          canManage ? (
            <CreateDialog<FunctionFormValues>
              open={createOpen}
              onOpenChange={setCreateOpen}
              triggerLabel="Create function"
              submitLabel="Create function"
              pendingLabel="Creating function…"
              title="Create a function"
              description="The function definition is stored by the Go API. Source deployment happens separately through an archive."
              fields={functionFields}
              pending={create.isPending}
              onSubmit={handleCreateFunction}
            />
          ) : query.data ? (
            <Badge variant="neutral">Read-only</Badge>
          ) : null
        }
      />
      <ProjectResourceIntro
        icon={FunctionSquare}
        title="Functions"
        description="Deploy immutable source archives, then inspect builds and executions."
      />
      {query.isError ? (
        <ErrorState
          title="Could not load functions"
          error={query.error}
          retry={() => query.refetch()}
        />
      ) : query.data?.functions.length ? (
        <ResourceTableCard
          data={query.data.functions}
          searchable={(item, term) =>
            `${item.name} ${item.runtime} ${item.status}`
              .toLowerCase()
              .includes(term)
          }
          serverPagination={pageControls(
            navigation,
            nextCursor(query.data),
            query.isFetching,
          )}
        >
          {(filtered, pagination) => (
            <DataTable
              data={filtered}
              columns={columns}
              serverPagination={pagination}
            />
          )}
        </ResourceTableCard>
      ) : query.isLoading ? (
        <Card>
          <DataTable data={[]} columns={columns} loading />
        </Card>
      ) : (
        <EmptyState
          title="No functions yet"
          description={
            canManage
              ? "Functions run backend workloads on demand. Create one to deploy your first workload."
              : "No functions are available to manage in this project."
          }
          actionLabel={canManage ? "Create function" : undefined}
          action={canManage ? () => setCreateOpen(true) : undefined}
        />
      )}
    </>
  );
}
