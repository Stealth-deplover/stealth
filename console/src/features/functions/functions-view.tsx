"use client";
import Link from "next/link";
import { type ColumnDef } from "@tanstack/react-table";
import { FunctionSquare } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { useCreateFunction } from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { useFunctions } from "@/api/queries";
import type { StealthFunction } from "@/api/types";
import type { components } from "@/api/generated/schema";
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
import {
  FUNCTION_RUNTIME_OPTIONS,
  isFunctionRuntime,
} from "@/lib/capabilities/runtime-catalog";
import { ProjectResourceIntro } from "@/features/resources/collection-shared";
import { pageControls } from "@/lib/pagination";

export function FunctionsView({
  organizationId,
  projectId,
}: {
  organizationId: string;
  projectId: string;
}) {
  const navigation = useCursorPagination();
  const [createOpen, setCreateOpen] = useState(false);
  const query = useFunctions(projectId, { cursor: navigation.cursor });
  const create = useCreateFunction(projectId);
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const handleCreateFunction = async (values: Record<string, string>) => {
    if (!isFunctionRuntime(values.runtime)) {
      toast.error("Runtime is not supported by the current Stealth API.");
      return;
    }
    await create.mutateAsync({
      name: values.name,
      runtime: values.runtime as components["schemas"]["FunctionRuntime"],
      entrypoint: values.entrypoint,
      commands: "",
      timeout_seconds: 15,
      enabled: true,
      logging: true,
      description: values.description,
    });
    toast.success("Function created");
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
          <Badge variant="success">active</Badge>
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
          <CreateDialog
            open={createOpen}
            onOpenChange={setCreateOpen}
            triggerLabel="Create function"
            submitLabel="Create function"
            pendingLabel="Creating function…"
            title="Create a function"
            description="The function definition is stored by the Go API. Source deployment happens separately through an archive."
            fields={[
              { name: "name", label: "Name", placeholder: "api-handler" },
              {
                name: "runtime",
                label: "Runtime",
                type: "select",
                defaultValue: FUNCTION_RUNTIME_OPTIONS[0].value,
                options: FUNCTION_RUNTIME_OPTIONS,
                help: "Available runtimes are centralized from the current OpenAPI enum.",
              },
              {
                name: "entrypoint",
                label: "Entrypoint",
                placeholder: "src/index.main",
              },
              {
                name: "description",
                label: "Description",
                type: "textarea",
                required: false,
              },
            ]}
            pending={create.isPending}
            onSubmit={handleCreateFunction}
          />
        }
      />
      <ProjectResourceIntro
        icon={FunctionSquare}
        title="Functions"
        description="Deploy immutable source archives, then inspect builds and executions."
      />
      {query.isError ? (
        <ErrorState error={query.error} retry={() => query.refetch()} />
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
          description="Functions run backend workloads on demand. Create one to deploy your first workload."
          actionLabel="Create function"
          action={() => setCreateOpen(true)}
        />
      )}
    </>
  );
}
