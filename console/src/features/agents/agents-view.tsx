"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { type ColumnDef } from "@tanstack/react-table";
import { Activity, Bot, Play, Settings2, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import {
  useCreateAgent,
  useDeleteAgent,
  useUpdateAgent,
} from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import {
  useAgent,
  useAgentCatalog,
  useAgentRuns,
  useAgents,
} from "@/api/queries";
import type { Agent, AgentCatalog, AgentRun } from "@/api/types";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import {
  agentFields,
  agentPayload,
  type AgentFormValues,
} from "@/features/agents/agent-form";
import { formatAgentRunDuration } from "@/features/agents/agent-run-state";
import { AgentRunStatusBadge } from "@/features/agents/agent-run-status-badge";
import { formatDate } from "@/lib/format";

function catalogReady(catalog: AgentCatalog | undefined) {
  return Boolean(
    catalog?.providers.length && catalog.roles.length && catalog.tools.length,
  );
}

function AgentRunSummary({ run, base }: { run: AgentRun; base: string }) {
  return (
    <Link
      href={`${base}/runs/${run.id}`}
      className="block rounded-lg border border-stealth-border p-3 hover:bg-white/[0.04]"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <AgentRunStatusBadge status={run.status} />
        <span className="text-xs text-slate-600">
          {formatDate(run.created_at)}
        </span>
      </div>
      <p className="mt-2 truncate text-sm text-slate-200">{run.prompt}</p>
      <p className="mt-1 text-xs text-slate-600">
        {formatAgentRunDuration(run)} · {run.id}
      </p>
    </Link>
  );
}

export function AgentsView({
  organizationId,
  projectId,
}: {
  organizationId: string;
  projectId: string;
}) {
  const router = useRouter();
  const navigation = useCursorPagination();
  const query = useAgents(projectId, { cursor: navigation.cursor });
  const catalog = useAgentCatalog();
  const create = useCreateAgent(projectId);
  const [createOpen, setCreateOpen] = useState(false);
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const ready = catalogReady(catalog.data);
  const agents = query.data?.agents ?? [];
  const canManage = query.data?.can_manage === true;

  const handleCreateAgent = async (values: AgentFormValues) => {
    const result = await create.mutateAsync({
      project_id: projectId,
      ...agentPayload(catalog.data, values),
    });
    if (!result?.agent.id) {
      throw new Error("The API did not return the created agent.");
    }
    toast.success("Agent created");
    router.push(`${base}/agents/${result.agent.id}`);
  };

  const columns: ColumnDef<Agent, unknown>[] = [
    {
      accessorKey: "name",
      header: "Agent",
      cell: ({ row }) => (
        <Link
          href={`${base}/agents/${row.original.id}`}
          className="block min-w-48 font-medium text-white hover:text-orange-200"
        >
          <span className="block">{row.original.name}</span>
          <span
            className="mt-0.5 block max-w-sm truncate text-[11px] text-slate-500"
            title={row.original.description}
          >
            {row.original.description || "No description"}
          </span>
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
      id: "runtime",
      header: "Runtime",
      cell: ({ row }) => (
        <span className="font-mono text-xs text-slate-400">
          {row.original.provider}/{row.original.model}
        </span>
      ),
    },
    {
      accessorKey: "created_at",
      header: "Created",
      cell: ({ row }) => formatDate(row.original.created_at),
    },
    {
      accessorKey: "updated_at",
      header: "Updated",
      cell: ({ row }) => formatDate(row.original.updated_at),
    },
  ];

  const hasRows =
    agents.length > 0 || navigation.canFirst || Boolean(nextCursor(query.data));

  return (
    <>
      <PageHeader
        eyebrow="Developer tooling"
        title="Agents"
        description="Run backend-owned developer tasks through durable Agent Runs."
        actions={
          canManage && ready ? (
            <CreateDialog<AgentFormValues>
              open={createOpen}
              onOpenChange={setCreateOpen}
              triggerLabel="Create agent"
              submitLabel="Create agent"
              pendingLabel="Creating agent…"
              title="Create an agent"
              description={
                catalog.data?.execution.message ??
                "Configure a durable task runner from the server catalog."
              }
              fields={agentFields(catalog.data)}
              pending={create.isPending}
              onSubmit={handleCreateAgent}
            />
          ) : null
        }
      />
      {catalog.error ? (
        <ErrorState
          title="Could not load agent catalog"
          error={catalog.error}
          retry={() => catalog.refetch()}
        />
      ) : (
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
      )}
      {query.error ? (
        <ErrorState
          title="Could not load agents"
          error={query.error}
          retry={() => query.refetch()}
        />
      ) : hasRows ? (
        <Card>
          <DataTable
            data={agents}
            columns={columns}
            loading={query.isLoading}
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
      ) : query.isLoading ? (
        <Card>
          <DataTable data={[]} columns={columns} loading />
        </Card>
      ) : (
        <EmptyState
          title="No agents yet"
          description={
            canManage
              ? "Create an agent to run developer tasks."
              : "No agents are available to manage in this project."
          }
          action={ready && canManage ? () => setCreateOpen(true) : undefined}
          actionLabel={ready && canManage ? "Create agent" : undefined}
        />
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
  const router = useRouter();
  const query = useAgent(agentId);
  const catalog = useAgentCatalog();
  const runs = useAgentRuns(agentId);
  const update = useUpdateAgent(projectId, agentId);
  const remove = useDeleteAgent(projectId, agentId);
  const agent = query.data?.agent;
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const ready = catalogReady(catalog.data);
  const latestRun = runs.data?.runs[0];
  const canManage = query.data?.can_manage === true;

  if (query.error)
    return (
      <ErrorState
        title="Could not load agent"
        error={query.error}
        retry={() => query.refetch()}
      />
    );
  if (query.isLoading) return <LoadingState rows={4} />;
  if (!agent)
    return (
      <EmptyState
        title="Agent not found"
        description="The agent may have been removed or is outside this project."
      />
    );

  const handleUpdate = async (values: AgentFormValues) => {
    await update.mutateAsync(agentPayload(catalog.data, values));
    toast.success("Agent configuration updated");
  };

  const handleDelete = async () => {
    await remove.mutateAsync();
    toast.success("Agent deleted");
    router.replace(`${base}/agents`);
  };

  return (
    <>
      <div className="mb-5">
        <Link
          href={`${base}/agents`}
          className="text-xs text-slate-500 hover:text-slate-200"
        >
          ← Back to agents
        </Link>
      </div>
      <PageHeader
        eyebrow="Agent"
        title={agent.name}
        description={agent.description || "Durable developer task runner."}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <StatusBadge status={agent.status} />
            {canManage ? (
              <>
                <Button asChild size="sm">
                  <Link href={`${base}/agents/${agentId}/runs`}>
                    <Play className="size-3.5" /> Run agent
                  </Link>
                </Button>
                {ready ? (
                  <CreateDialog<AgentFormValues>
                    triggerLabel="Edit configuration"
                    submitLabel="Save changes"
                    pendingLabel="Saving changes…"
                    title="Edit agent configuration"
                    description="Configuration fields are persisted by the Go API. Runtime status and activity remain backend-owned."
                    fields={agentFields(catalog.data, agent)}
                    pending={update.isPending}
                    trigger={
                      <Button variant="outline" size="sm">
                        <Settings2 className="size-3.5" /> Edit configuration
                      </Button>
                    }
                    onSubmit={handleUpdate}
                  />
                ) : null}
                <ConfirmDialog
                  trigger={
                    <Button
                      variant="destructive"
                      size="sm"
                      disabled={remove.isPending}
                    >
                      <Trash2 className="size-3.5" /> Delete agent
                    </Button>
                  }
                  title="Delete agent?"
                  description="The backend deletes this agent's run history with the agent."
                  confirmLabel="Delete agent"
                  pending={remove.isPending}
                  onConfirm={handleDelete}
                />
              </>
            ) : null}
          </div>
        }
      />
      <div className="mb-5 flex flex-wrap items-center gap-3 text-xs text-slate-500">
        <ResourceId id={agent.id} label="Agent ID" />
        <span>Created {formatDate(agent.created_at)}</span>
        <span>Updated {formatDate(agent.updated_at)}</span>
      </div>

      <Card className="mb-5">
        <CardHeader>
          <CardTitle>Overview</CardTitle>
        </CardHeader>
        <CardContent>
          <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <dt className="text-xs text-slate-600">Status</dt>
              <dd className="mt-2">
                <StatusBadge status={agent.status} />
              </dd>
            </div>
            <div>
              <dt className="text-xs text-slate-600">Role</dt>
              <dd className="mt-2 text-sm text-white">{agent.role}</dd>
            </div>
            <div>
              <dt className="text-xs text-slate-600">Runtime</dt>
              <dd className="mt-2 font-mono text-xs text-slate-300">
                {agent.provider}/{agent.model}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-slate-600">Current task</dt>
              <dd className="mt-2 text-sm text-white">
                {agent.current_task ?? "Idle"}
              </dd>
            </div>
          </dl>
        </CardContent>
      </Card>

      <div className="grid gap-5 xl:grid-cols-[1fr_1.1fr]">
        <Card>
          <CardHeader>
            <CardTitle>Configuration</CardTitle>
          </CardHeader>
          <CardContent>
            <dl className="grid gap-4 sm:grid-cols-2">
              <div>
                <dt className="text-xs text-slate-600">Branch</dt>
                <dd className="mt-1 font-mono text-xs text-slate-300">
                  {agent.branch}
                </dd>
              </div>
              <div>
                <dt className="text-xs text-slate-600">Tools</dt>
                <dd className="mt-1 text-sm text-slate-300">
                  {agent.tools.length
                    ? agent.tools.join(", ")
                    : "None configured"}
                </dd>
              </div>
            </dl>
            <div className="mt-5">
              <p className="text-xs text-slate-600">Instructions</p>
              <p className="mt-1 whitespace-pre-wrap text-sm leading-6 text-slate-300">
                {agent.instructions ?? "No additional instructions configured."}
              </p>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex-row items-center justify-between">
            <CardTitle>Runs</CardTitle>
            <Button asChild variant="ghost" size="sm">
              <Link href={`${base}/agents/${agentId}/runs`}>
                <Activity className="size-3.5" /> View all
              </Link>
            </Button>
          </CardHeader>
          <CardContent>
            {runs.error ? (
              <p className="text-sm text-rose-200">
                Could not load agent runs. Try the runs view again.
              </p>
            ) : latestRun ? (
              <AgentRunSummary
                run={latestRun}
                base={`${base}/agents/${agentId}/runs`}
              />
            ) : runs.isPending ? (
              <p className="text-sm text-slate-500">Loading latest run…</p>
            ) : (
              <div>
                <p className="text-sm text-white">No runs yet</p>
                <p className="mt-1 text-sm text-slate-500">
                  {canManage
                    ? "Run this agent to start its first task."
                    : "No runs have been created for this agent."}
                </p>
                {canManage ? (
                  <Button asChild className="mt-4" size="sm">
                    <Link href={`${base}/agents/${agentId}/runs`}>
                      <Play className="size-3.5" /> Run agent
                    </Link>
                  </Button>
                ) : null}
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      {catalog.error ? (
        <p className="mt-5 text-xs text-amber-200">
          Could not load the server catalog, so configuration editing is
          unavailable.
        </p>
      ) : null}
    </>
  );
}
