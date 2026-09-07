"use client";
import Link from "next/link";
import { type ColumnDef } from "@tanstack/react-table";
import { Activity, Bot } from "lucide-react";
import { toast } from "sonner";
import { useCreateAgent } from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { useAgent, useAgentCatalog, useAgents } from "@/api/queries";
import type { Agent } from "@/api/types";
import type { components } from "@/api/generated/schema";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { StatusBadge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";

export function AgentsView({
  organizationId,
  projectId,
}: {
  organizationId: string;
  projectId: string;
}) {
  const navigation = useCursorPagination();
  const query = useAgents(projectId, { cursor: navigation.cursor });
  const catalog = useAgentCatalog();
  const create = useCreateAgent(projectId);
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const provider = catalog.data?.providers[0];
  const roleOptions = (catalog.data?.roles ?? []).map((role) => ({
    value: role,
    label: role,
  }));
  const providerOptions = (catalog.data?.providers ?? []).map((item) => ({
    value: item.id,
    label: item.name,
  }));
  const modelOptions = [
    ...new Set((catalog.data?.providers ?? []).flatMap((item) => item.models)),
  ].map((model) => ({ value: model, label: model }));
  const catalogReady = Boolean(
    catalog.data &&
    provider &&
    roleOptions.length &&
    providerOptions.length &&
    modelOptions.length,
  );
  const handleCreateAgent = async (values: Record<string, string>) => {
    await create.mutateAsync({
      project_id: projectId,
      name: values.name,
      role: values.role as components["schemas"]["AgentRole"],
      provider: values.provider,
      model: values.model,
      branch: values.branch,
    });
    toast.success("Agent created");
  };
  const columns: ColumnDef<Agent, unknown>[] = [
    {
      accessorKey: "name",
      header: "Agent",
      cell: ({ row }) => (
        <Link
          href={`${base}/agents/${row.original.id}`}
          className="font-medium text-white hover:text-orange-200"
        >
          {row.original.name}
        </Link>
      ),
    },
    { accessorKey: "role", header: "Role" },
    {
      accessorKey: "status",
      header: "Status",
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: "provider",
      header: "Provider",
      cell: ({ row }) => (
        <span className="font-mono text-xs text-slate-400">
          {row.original.provider}/{row.original.model}
        </span>
      ),
    },
    {
      accessorKey: "last_active_at",
      header: "Last active",
      cell: ({ row }) => formatDate(row.original.last_active_at),
    },
  ];
  return (
    <>
      <PageHeader
        eyebrow="Developer tooling"
        title="Agents"
        description="Run backend-owned coding tasks with catalog-backed providers and models."
        actions={
          <CreateDialog
            triggerLabel="Create agent"
            submitLabel="Create agent"
            pendingLabel="Creating agent…"
            title="Create an agent"
            description={
              catalog.data?.execution.message ??
              "Configure a durable agent run target."
            }
            disabled={!catalogReady}
            fields={[
              { name: "name", label: "Name", placeholder: "frontend-reviewer" },
              {
                name: "role",
                label: "Role",
                type: "select",
                defaultValue: catalog.data?.roles[0] ?? "",
                options: roleOptions,
              },
              {
                name: "provider",
                label: "Provider",
                type: "select",
                defaultValue: provider?.id ?? "",
                options: providerOptions,
              },
              {
                name: "model",
                label: "Model",
                type: "select",
                defaultValue: provider?.models[0] ?? "",
                options: modelOptions,
                help: "Models are offered by the server catalog; provider/model compatibility remains backend-owned.",
              },
              { name: "branch", label: "Branch", defaultValue: "main" },
            ]}
            pending={create.isPending}
            onSubmit={handleCreateAgent}
          />
        }
      />
      <Card className="mb-5 border-orange-300/15 bg-orange-300/[0.03]">
        <CardContent className="flex items-start gap-3 p-5">
          <Bot className="mt-0.5 size-5 text-orange-300" />
          <div>
            <p className="text-sm font-medium text-orange-100">
              Execution mode: {catalog.data?.execution.mode ?? "loading"}
            </p>
            <p className="mt-1 text-xs leading-5 text-orange-200/60">
              {catalog.data?.execution.message ?? "Reading agent catalog…"}
            </p>
          </div>
        </CardContent>
      </Card>
      {query.isError ? (
        <ErrorState error={query.error} retry={() => query.refetch()} />
      ) : (
        <Card>
          <DataTable
            data={query.data?.agents ?? []}
            columns={columns}
            loading={query.isLoading}
            empty="No agents yet. Create a task runner when the API catalog is ready."
            serverPagination={{
              canFirst: navigation.canFirst,
              canPrevious: navigation.canPrevious,
              canNext: Boolean(nextCursor(query.data)),
              onFirst: navigation.goFirst,
              onPrevious: navigation.goPrevious,
              onNext: () => navigation.goNext(nextCursor(query.data)),
              isFetching: query.isFetching,
            }}
          />
        </Card>
      )}
    </>
  );
}

export function AgentDetailView({
  organizationId,
  projectId,
  agentId,
}: {
  organizationId: string;
  projectId: string;
  agentId: string;
}) {
  const query = useAgent(agentId);
  const agent = query.data?.agent;
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  if (query.error)
    return <ErrorState error={query.error} retry={() => query.refetch()} />;
  if (query.isLoading) return <LoadingState rows={4} />;
  if (!agent)
    return (
      <EmptyState
        title="Agent not found"
        description="The agent may have been removed or is outside this project."
      />
    );
  return (
    <>
      <PageHeader
        eyebrow="Agent"
        title={agent.name}
        description={`${agent.role} · ${agent.provider}/${agent.model}`}
        actions={<StatusBadge status={agent.status} />}
      />
      <div className="mb-5 flex flex-wrap items-center gap-2">
        <span className="text-xs text-slate-600">
          Updated {formatDate(agent.updated_at)}
        </span>
        <ResourceId id={agent.id} label="Agent ID" />
      </div>
      <div className="grid gap-4 md:grid-cols-3">
        <Card>
          <CardContent className="p-5">
            <p className="text-xs text-slate-500">Branch</p>
            <p className="mt-2 font-mono text-sm text-white">{agent.branch}</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-5">
            <p className="text-xs text-slate-500">Tools</p>
            <p className="mt-2 text-sm text-white">
              {agent.tools.length} enabled
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-5">
            <p className="text-xs text-slate-500">Current task</p>
            <p className="mt-2 text-sm text-white">
              {agent.current_task ?? "Idle"}
            </p>
          </CardContent>
        </Card>
      </div>
      <div className="mt-5">
        <Link
          href={`${base}/agents/${agentId}/runs`}
          className="inline-flex items-center gap-2 rounded-lg border border-stealth-border px-3.5 py-2 text-sm text-slate-300 hover:bg-white/[0.05]"
        >
          <Activity className="size-4" /> Open run history
        </Link>
      </div>
    </>
  );
}
