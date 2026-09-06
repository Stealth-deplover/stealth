"use client";

import Link from "next/link";
import { type ColumnDef } from "@tanstack/react-table";
import { Activity, Bot, Check, CircleHelp, Settings2, TerminalSquare } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { useCreateAgent, useUpdateAuthSettings } from "@/api/mutations";
import { useAgentCatalog, useAgents, useAuthSettings, useFunctions, useProject, useProjectTraces, useSites, useMessagingMessages, useMessagingProviders, useMessagingTopics } from "@/api/queries";
import type { Agent } from "@/api/types";
import type { components } from "@/api/generated/schema";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { formatDate, formatDuration } from "@/lib/format";

export function DeploymentsView({ organizationId, projectId }: { organizationId: string; projectId: string }) {
  const functions = useFunctions(projectId);
  const sites = useSites(projectId);
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  if (functions.error || sites.error) return <ErrorState error={functions.error ?? sites.error} retry={() => { void functions.refetch(); void sites.refetch(); }} />;
  return <><PageHeader eyebrow="Compute" title="Deployments" description="The backend exposes deployment history under each Function and Site. This page links the actual resource-scoped views instead of inventing a global deployment endpoint." /><div className="grid gap-4 md:grid-cols-2"><Card><CardHeader><CardTitle>Functions</CardTitle></CardHeader><CardContent className="space-y-3">{functions.data?.functions.length ? functions.data.functions.map((item) => <Link key={item.id} href={`${base}/functions/${item.id}`} className="flex items-center justify-between rounded-lg border border-stealth-border p-3 hover:border-cyan-300/30"><span><span className="block text-sm font-medium text-white">{item.name}</span><span className="text-xs text-slate-600">{item.active_deployment_id ? "Active deployment available" : "No active deployment"}</span></span><StatusBadge status={item.active_deployment_id ? "active" : "inactive"} /></Link>) : <p className="text-sm text-slate-500">No functions returned.</p>}</CardContent></Card><Card><CardHeader><CardTitle>Sites</CardTitle></CardHeader><CardContent className="space-y-3">{sites.data?.sites.length ? sites.data.sites.map((item) => <Link key={item.id} href={`${base}/sites/${item.id}`} className="flex items-center justify-between rounded-lg border border-stealth-border p-3 hover:border-violet-300/30"><span><span className="block text-sm font-medium text-white">{item.name}</span><span className="text-xs text-slate-600">{item.active_deployment_id ? "Active deployment available" : "No active deployment"}</span></span><StatusBadge status={item.active_deployment_id ? "active" : "inactive"} /></Link>) : <p className="text-sm text-slate-500">No sites returned.</p>}</CardContent></Card></div></>;
}

export function TracesView({ projectId }: { projectId: string }) {
  const query = useProjectTraces(projectId);
  if (query.error) return <ErrorState error={query.error} retry={() => query.refetch()} />;
  return <><PageHeader eyebrow="Observability" title="Traces" description="Root HTTP request index persisted by the backend. Filters are limited to the API contract currently available." /><Card><DataTable data={query.data?.traces ?? []} loading={query.isLoading} columns={[{ accessorKey: "started_at", header: "Timestamp", cell: ({ row }) => formatDate(row.original.started_at) }, { accessorKey: "service", header: "Service" }, { accessorKey: "method", header: "Method", cell: ({ row }) => <span className="font-mono text-xs text-cyan-200">{row.original.method}</span> }, { accessorKey: "route", header: "Route", cell: ({ row }) => <span className="font-mono text-xs text-slate-400">{row.original.route}</span> }, { accessorKey: "status", header: "Status", cell: ({ row }) => <StatusBadge status={row.original.status >= 500 ? "error" : row.original.status >= 400 ? "warning" : "success"} /> }, { accessorKey: "duration_ms", header: "Duration", cell: ({ row }) => formatDuration(row.original.duration_ms) }, { accessorKey: "trace_id", header: "Trace ID", cell: ({ row }) => <span className="font-mono text-xs text-slate-500">{row.original.trace_id}</span> }]} empty="No traces returned." /></Card></>;
}

export function LogsView({ organizationId, projectId }: { organizationId: string; projectId: string }) {
  const functions = useFunctions(projectId);
  const sites = useSites(projectId);
  if (functions.error || sites.error) return <ErrorState error={functions.error ?? sites.error} retry={() => { void functions.refetch(); void sites.refetch(); }} />;
  return <><PageHeader eyebrow="Observability" title="Logs" description="Logs are contextual to Function builds/executions, Site builds, and Agent runs. Pick a resource to preserve the backend's incremental cursor semantics." /><div className="grid gap-4 md:grid-cols-3">{functions.data?.functions.slice(0, 6).map((item) => <Card key={item.id}><CardContent className="p-5"><FunctionSquareIcon /><h2 className="mt-4 text-sm font-semibold text-white">{item.name}</h2><p className="mt-1 text-xs leading-5 text-slate-500">Build and execution logs are available from the Function detail view.</p><Link href={`/organizations/${organizationId}/projects/${projectId}/functions/${item.id}`} className="mt-4 inline-flex text-xs text-cyan-300">Open function →</Link></CardContent></Card>)}{sites.data?.sites.slice(0, 3).map((item) => <Card key={item.id}><CardContent className="p-5"><TerminalSquare className="size-5 text-violet-300" /><h2 className="mt-4 text-sm font-semibold text-white">{item.name}</h2><p className="mt-1 text-xs leading-5 text-slate-500">Site build logs are scoped to each site deployment.</p><Link href={`/organizations/${organizationId}/projects/${projectId}/sites/${item.id}`} className="mt-4 inline-flex text-xs text-violet-300">Open site →</Link></CardContent></Card>)}</div>{!functions.data?.functions.length && !sites.data?.sites.length ? <div className="mt-4"><EmptyState title="No log sources yet" description="Create a Function, Site, or Agent run to get a backend-owned incremental log stream." /></div> : null}</>;
}

function FunctionSquareIcon() { return <TerminalSquare className="size-5 text-cyan-300" />; }

export function AgentsView({ organizationId, projectId }: { organizationId: string; projectId: string }) {
  const query = useAgents(projectId);
  const catalog = useAgentCatalog();
  const create = useCreateAgent(projectId);
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const provider = catalog.data?.providers[0];
  const columns: ColumnDef<Agent, unknown>[] = [{ accessorKey: "name", header: "Agent", cell: ({ row }) => <Link href={`${base}/agents/${row.original.id}`} className="font-medium text-white hover:text-orange-200">{row.original.name}</Link> }, { accessorKey: "role", header: "Role" }, { accessorKey: "status", header: "Status", cell: ({ row }) => <StatusBadge status={row.original.status} /> }, { accessorKey: "provider", header: "Provider", cell: ({ row }) => <span className="font-mono text-xs text-slate-400">{row.original.provider}/{row.original.model}</span> }, { accessorKey: "last_active_at", header: "Last active", cell: ({ row }) => formatDate(row.original.last_active_at) }];
  return <><PageHeader eyebrow="Developer tooling" title="Agents" description="Coding agents are modeled as durable task runners. Their execution readiness comes from the API catalog." actions={<CreateDialog title="Create an agent" description={catalog.data?.execution.message ?? "Configure a durable agent run target."} fields={[{ name: "name", label: "Name", placeholder: "frontend-reviewer" }, { name: "role", label: "Role", defaultValue: "General" }, { name: "provider", label: "Provider", defaultValue: provider?.id ?? "provider" }, { name: "model", label: "Model", defaultValue: provider?.models[0] ?? "model" }, { name: "branch", label: "Branch", defaultValue: "main" }]} pending={create.isPending} onSubmit={async (values) => { await create.mutateAsync({ project_id: projectId, name: values.name, role: values.role as components["schemas"]["AgentRole"], provider: values.provider, model: values.model, branch: values.branch }); toast.success("Agent created"); }} />} /><Card className="mb-5 border-orange-300/15 bg-orange-300/[0.03]"><CardContent className="flex items-start gap-3 p-5"><Bot className="mt-0.5 size-5 text-orange-300" /><div><p className="text-sm font-medium text-orange-100">Execution mode: {catalog.data?.execution.mode ?? "loading"}</p><p className="mt-1 text-xs leading-5 text-orange-200/60">{catalog.data?.execution.message ?? "Reading agent catalog…"}</p></div></CardContent></Card>{query.isError ? <ErrorState error={query.error} retry={() => query.refetch()} /> : <Card><DataTable data={query.data?.agents ?? []} columns={columns} loading={query.isLoading} empty="No agents in this project yet." /></Card>}</>;
}

export function AgentDetailView({ organizationId, projectId, agentId }: { organizationId: string; projectId: string; agentId: string }) {
  const query = useAgents(projectId);
  const agent = query.data?.agents.find((item) => item.id === agentId);
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  if (query.error) return <ErrorState error={query.error} retry={() => query.refetch()} />;
  if (query.isLoading) return <LoadingState rows={4} />;
  if (!agent) return <EmptyState title="Agent not found" description="The agent may have been removed or is outside this project." />;
  return <><PageHeader eyebrow="Agent" title={agent.name} description={`${agent.role} · ${agent.provider}/${agent.model}`} actions={<StatusBadge status={agent.status} />} /><div className="grid gap-4 md:grid-cols-3"><Card><CardContent className="p-5"><p className="text-xs text-slate-500">Branch</p><p className="mt-2 font-mono text-sm text-white">{agent.branch}</p></CardContent></Card><Card><CardContent className="p-5"><p className="text-xs text-slate-500">Tools</p><p className="mt-2 text-sm text-white">{agent.tools.length} enabled</p></CardContent></Card><Card><CardContent className="p-5"><p className="text-xs text-slate-500">Current task</p><p className="mt-2 text-sm text-white">{agent.current_task ?? "Idle"}</p></CardContent></Card></div><div className="mt-5"><Link href={`${base}/agents/${agentId}/runs`} className="inline-flex items-center gap-2 rounded-lg border border-stealth-border px-3.5 py-2 text-sm text-slate-300 hover:bg-white/[0.05]"><Activity className="size-4" /> Open run history</Link></div></>;
}

export function MessagingView({ projectId }: { projectId: string }) {
  const providers = useMessagingProviders(projectId);
  const topics = useMessagingTopics(projectId);
  const messages = useMessagingMessages(projectId);
  const error = providers.error ?? topics.error ?? messages.error;
  if (error) return <ErrorState error={error} retry={() => { void providers.refetch(); void topics.refetch(); void messages.refetch(); }} />;
  return <><PageHeader eyebrow="Integrations" title="Messaging" description="Provider credentials and message content stay protected by the backend. The console exposes safe metadata only." /><div className="grid gap-4 md:grid-cols-3"><Card><CardContent className="p-5"><p className="text-xs text-slate-500">Providers</p><p className="mt-2 text-2xl font-semibold text-white">{providers.data?.providers.length ?? "—"}</p><p className="mt-2 text-xs text-slate-600">Encrypted credentials are never returned.</p></CardContent></Card><Card><CardContent className="p-5"><p className="text-xs text-slate-500">Topics</p><p className="mt-2 text-2xl font-semibold text-white">{topics.data?.topics.length ?? "—"}</p></CardContent></Card><Card><CardContent className="p-5"><p className="text-xs text-slate-500">Messages</p><p className="mt-2 text-2xl font-semibold text-white">{messages.data?.messages.length ?? "—"}</p><p className="mt-2 text-xs text-slate-600">Metadata only; content remains encrypted.</p></CardContent></Card></div><Card className="mt-5"><CardHeader><CardTitle>Topics</CardTitle></CardHeader><CardContent className="space-y-2">{topics.data?.topics.length ? topics.data.topics.map((topic) => <div key={topic.id} className="flex items-center justify-between rounded-lg border border-stealth-border px-3 py-3"><div><p className="text-sm font-medium text-white">{topic.name}</p><p className="text-xs text-slate-600">{topic.subscriber_count} subscribers</p></div><StatusBadge status={topic.enabled ? "active" : "inactive"} /></div>) : <p className="text-sm text-slate-500">No topics returned.</p>}</CardContent></Card></>;
}

export function AuthSettingsView({ projectId }: { projectId: string }) {
  const query = useAuthSettings(projectId);
  const update = useUpdateAuthSettings(projectId);
  const [origins, setOrigins] = useState<string | null>(null);
  if (query.error) return <ErrorState error={query.error} retry={() => query.refetch()} />;
  if (query.isLoading) return <LoadingState rows={4} />;
  const settings = query.data?.settings;
  if (!settings) return <EmptyState title="Auth settings unavailable" description="The API did not return project Auth settings." />;
  const originValue = origins ?? settings.cors_origins.join("\n");
  return <><PageHeader eyebrow="Project Auth" title="Auth settings" description="Configure registration and explicit browser CORS origins for the project application API." /><Card><CardHeader><CardTitle>Registration</CardTitle></CardHeader><CardContent><div className="flex items-center justify-between rounded-lg border border-stealth-border p-4"><div><p className="text-sm font-medium text-white">Allow registrations</p><p className="mt-1 text-xs text-slate-500">The project application API enforces this setting.</p></div><Button variant={settings.registration_enabled ? "default" : "outline"} onClick={() => update.mutate({ registration_enabled: !settings.registration_enabled }, { onSuccess: () => toast.success("Auth setting updated") })}>{settings.registration_enabled ? "Enabled" : "Disabled"}</Button></div></CardContent></Card><Card className="mt-5"><CardHeader><CardTitle>Browser CORS origins</CardTitle></CardHeader><CardContent><Label htmlFor="cors">One explicit HTTP(S) origin per line</Label><Textarea id="cors" className="mt-2" value={originValue} onChange={(event) => setOrigins(event.target.value)} placeholder="https://app.example.com" /><Button className="mt-3" disabled={update.isPending} onClick={() => update.mutate({ cors_origins: originValue.split("\n").map((origin) => origin.trim()).filter(Boolean) }, { onSuccess: () => toast.success("Origins saved") })}><Settings2 className="size-4" /> Save origins</Button></CardContent></Card></>;
}

export function ProjectSettingsView({ projectId }: { projectId: string }) {
  const project = useProject(projectId);
  if (project.isLoading) return <LoadingState rows={4} />;
  return <><PageHeader eyebrow="Project" title="Settings" description="Project metadata and backend-owned configuration boundaries." />{project.error ? <ErrorState error={project.error} retry={() => project.refetch()} /> : <div className="grid gap-4 md:grid-cols-2"><Card><CardHeader><CardTitle>Project identity</CardTitle></CardHeader><CardContent><dl className="space-y-3 text-sm"><div><dt className="text-xs text-slate-600">Name</dt><dd className="mt-1 text-white">{project.data?.project.name}</dd></div><div><dt className="text-xs text-slate-600">Project ID</dt><dd className="mt-1 break-all font-mono text-xs text-slate-400">{project.data?.project.id}</dd></div><div><dt className="text-xs text-slate-600">Created</dt><dd className="mt-1 text-slate-300">{formatDate(project.data?.project.created_at)}</dd></div></dl></CardContent></Card><Card><CardHeader><CardTitle>Backend boundaries</CardTitle></CardHeader><CardContent><ul className="space-y-3 text-sm leading-6 text-slate-500"><li className="flex gap-2"><Check className="mt-1 size-4 shrink-0 text-emerald-300" />Go API remains the source of truth.</li><li className="flex gap-2"><Check className="mt-1 size-4 shrink-0 text-emerald-300" />Console requests use the session cookie.</li><li className="flex gap-2"><CircleHelp className="mt-1 size-4 shrink-0 text-amber-300" />Source editor and dependency edges are not available in the current contract.</li></ul></CardContent></Card></div>}</>;
}
