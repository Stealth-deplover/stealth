"use client";

import Link from "next/link";
import type { ColumnDef } from "@tanstack/react-table";
import { ArrowLeft, FileUp, HardDrive, Play, Table2, Upload } from "lucide-react";
import { useCallback, useState } from "react";
import { toast } from "sonner";
import { api, unwrap } from "@/api/client";
import { useActivateFunctionDeployment, useCreateAgentRun, useUploadFunctionDeployment, useUploadSiteDeployment } from "@/api/mutations";
import { useAgentRuns, useDatabase, useDatabaseRows, useDatabaseTables, useFunction, useFunctionDeployment, useFunctionDeployments, useFunctionExecutions, useSite, useSiteDeployments, useStorageBucket, useStorageFiles, useWebhookDeliveries } from "@/api/queries";
import type { AgentRun, DatabaseRow, DatabaseTable, FunctionDeployment, FunctionExecution, SiteDeployment, StorageFile, WebhookDelivery } from "@/api/types";
import { DataTable } from "@/components/data-table";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { LogViewer, type LogLine } from "@/components/log-viewer";
import { PageHeader } from "@/components/page-header";
import { StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { formatBytes, formatDate, formatDuration } from "@/lib/format";

function BackLink({ href, label }: { href: string; label: string }) { return <Link href={href} className="mb-5 inline-flex items-center gap-2 text-xs text-slate-500 hover:text-cyan-200"><ArrowLeft className="size-3.5" /> {label}</Link>; }

export function FunctionDetailView({ organizationId, projectId, functionId }: { organizationId: string; projectId: string; functionId: string }) {
  const query = useFunction(projectId, functionId);
  const deployments = useFunctionDeployments(projectId, functionId);
  const executions = useFunctionExecutions(projectId, functionId);
  const upload = useUploadFunctionDeployment(projectId, functionId);
  const activate = useActivateFunctionDeployment(projectId, functionId);
  const [tab, setTab] = useState("overview");
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const fn = query.data?.function;
  const activeDeploymentId = fn?.active_deployment_id;
  const logs = useCallback(async (after?: number): Promise<LogLine[]> => {
    if (!activeDeploymentId) return [];
    const result = await api.GET("/v1/projects/{projectID}/functions/{functionID}/deployments/{deploymentID}/logs", { params: { path: { projectID: projectId, functionID: functionId, deploymentID: activeDeploymentId }, query: after === undefined ? {} : { after } } });
    const data = await unwrap(result);
    return (data?.logs ?? []) as LogLine[];
  }, [activeDeploymentId, functionId, projectId]);
  if (query.error) return <ErrorState error={query.error} retry={() => query.refetch()} />;
  if (!fn) return <EmptyState title="Function not found" description="The function may have been removed or is outside this project." />;
  const deploymentColumns: ColumnDef<FunctionDeployment, unknown>[] = [{ accessorKey: "version", header: "Version", cell: ({ row }) => <span className="font-mono text-xs">v{row.original.version}</span> }, { accessorKey: "status", header: "Status", cell: ({ row }) => <StatusBadge status={row.original.status} /> }, { accessorKey: "build_status", header: "Build", cell: ({ row }) => <StatusBadge status={row.original.build_status} /> }, { accessorKey: "created_at", header: "Created", cell: ({ row }) => formatDate(row.original.created_at) }, { id: "action", header: "", cell: ({ row }) => <div className="flex items-center gap-2"><Link href={`${base}/functions/${functionId}/deployments/${row.original.id}`} className="text-xs text-cyan-300">Inspect</Link>{row.original.status === "ready" ? <Button size="sm" variant="outline" onClick={() => activate.mutate(row.original.id, { onSuccess: () => toast.success("Deployment activated") })}>Activate</Button> : null}</div> }];
  const executionColumns: ColumnDef<FunctionExecution, unknown>[] = [{ accessorKey: "id", header: "Execution", cell: ({ row }) => <span className="font-mono text-xs text-slate-400">{row.original.id.slice(0, 12)}…</span> }, { accessorKey: "status", header: "Status", cell: ({ row }) => <StatusBadge status={row.original.status} /> }, { id: "duration", header: "Duration", cell: ({ row }) => row.original.started_at && row.original.finished_at ? formatDuration(new Date(row.original.finished_at).valueOf() - new Date(row.original.started_at).valueOf()) : "—" }, { accessorKey: "created_at", header: "Created", cell: ({ row }) => formatDate(row.original.created_at) }];
  return <><BackLink href={`${base}/functions`} label="Back to functions" /><PageHeader eyebrow="Function" title={fn.name} description={`${fn.runtime} · ${fn.entrypoint}`} actions={<label className="inline-flex h-9 cursor-pointer items-center gap-2 rounded-lg bg-cyan-300 px-3.5 text-sm font-medium text-slate-950 hover:bg-cyan-200"><Upload className="size-4" /> Deploy archive<input type="file" accept=".zip,.tar,.gz,.tgz" className="sr-only" onChange={(event) => { const file = event.target.files?.[0]; if (file) upload.mutate({ file }, { onSuccess: () => toast.success("Deployment queued") }); }} /></label>} /><div className="mb-5 flex items-center gap-2"><StatusBadge status={fn.status} /><span className="text-xs text-slate-600">Updated {formatDate(fn.updated_at)}</span></div><Tabs value={tab} onValueChange={setTab}><TabsList><TabsTrigger value="overview">Overview</TabsTrigger><TabsTrigger value="deployments">Deployments</TabsTrigger><TabsTrigger value="executions">Executions</TabsTrigger><TabsTrigger value="logs">Logs</TabsTrigger><TabsTrigger value="configuration">Configuration</TabsTrigger></TabsList><TabsContent value="overview"><div className="grid gap-4 md:grid-cols-3"><Card><CardContent className="p-5"><p className="text-xs text-slate-500">Runtime</p><p className="mt-2 font-mono text-sm text-white">{fn.runtime}</p></CardContent></Card><Card><CardContent className="p-5"><p className="text-xs text-slate-500">Active deployment</p><p className="mt-2 font-mono text-sm text-white">{fn.active_deployment_id ? `${fn.active_deployment_id.slice(0, 12)}…` : "Not deployed"}</p></CardContent></Card><Card><CardContent className="p-5"><p className="text-xs text-slate-500">Artifact usage</p><p className="mt-2 text-sm text-white">{formatBytes(fn.artifact_used_bytes)} / {formatBytes(fn.artifact_quota_bytes)}</p></CardContent></Card></div><Card className="mt-4"><CardHeader><CardTitle>Definition</CardTitle></CardHeader><CardContent><dl className="grid gap-4 sm:grid-cols-2"><div><dt className="text-xs text-slate-600">Entrypoint</dt><dd className="mt-1 font-mono text-sm text-slate-300">{fn.entrypoint}</dd></div><div><dt className="text-xs text-slate-600">Commands</dt><dd className="mt-1 font-mono text-sm text-slate-300">{fn.commands || "—"}</dd></div><div><dt className="text-xs text-slate-600">Timeout</dt><dd className="mt-1 text-sm text-slate-300">{fn.timeout_seconds}s</dd></div><div><dt className="text-xs text-slate-600">Logging</dt><dd className="mt-1 text-sm text-slate-300">{fn.logging ? "Enabled" : "Disabled"}</dd></div></dl></CardContent></Card></TabsContent><TabsContent value="deployments"><Card><DataTable data={deployments.data?.deployments ?? []} columns={deploymentColumns} loading={deployments.isLoading} empty="No deployments yet." /></Card></TabsContent><TabsContent value="executions"><Card><DataTable data={executions.data?.executions ?? []} columns={executionColumns} loading={executions.isLoading} empty="No executions yet." /></Card></TabsContent><TabsContent value="logs"><LogViewer title="Function build logs" description="Incremental deployment log stream" fetchPage={logs} enabled={Boolean(fn.active_deployment_id)} /></TabsContent><TabsContent value="configuration"><Card><CardContent className="p-6"><p className="text-sm text-slate-400">Environment variables are managed through the variables API. Source code is not exposed by the current backend contract, so this view does not invent an editor.</p></CardContent></Card></TabsContent></Tabs></>;
}

export function FunctionDeploymentView({ organizationId, projectId, functionId, deploymentId }: { organizationId: string; projectId: string; functionId: string; deploymentId: string }) {
  const query = useFunctionDeployment(projectId, functionId, deploymentId);
  const logFetcher = useCallback(async (after?: number): Promise<LogLine[]> => { const result = await api.GET("/v1/projects/{projectID}/functions/{functionID}/deployments/{deploymentID}/logs", { params: { path: { projectID: projectId, functionID: functionId, deploymentID: deploymentId }, query: after === undefined ? {} : { after } } }); const data = await unwrap(result); return (data?.logs ?? []) as LogLine[]; }, [deploymentId, functionId, projectId]);
  const deployment = query.data?.deployment;
  if (query.error) return <ErrorState error={query.error} retry={() => query.refetch()} />;
  if (!deployment) return <EmptyState title="Deployment not found" description="The deployment may have been removed or is outside this function." />;
  return <><BackLink href={`/organizations/${organizationId}/projects/${projectId}/functions/${functionId}`} label="Back to function" /><PageHeader eyebrow="Deployment" title={`Version ${deployment.version}`} description="Immutable deployment metadata and incremental build logs." actions={<StatusBadge status={deployment.status} />} /><div className="grid gap-4 md:grid-cols-4"><Card><CardContent className="p-4"><p className="text-xs text-slate-500">Build</p><div className="mt-2"><StatusBadge status={deployment.build_status} /></div></CardContent></Card><Card><CardContent className="p-4"><p className="text-xs text-slate-500">Source</p><p className="mt-2 text-sm text-white">{deployment.source}</p></CardContent></Card><Card><CardContent className="p-4"><p className="text-xs text-slate-500">Size</p><p className="mt-2 text-sm text-white">{formatBytes(deployment.size_bytes)}</p></CardContent></Card><Card><CardContent className="p-4"><p className="text-xs text-slate-500">Queued</p><p className="mt-2 text-sm text-white">{formatDate(deployment.queued_at)}</p></CardContent></Card></div><div className="mt-5"><LogViewer title="Build logs" description="Backend sequence cursor; only new lines are requested while following." fetchPage={logFetcher} /></div></>;
}

export function SiteDetailView({ organizationId, projectId, siteId }: { organizationId: string; projectId: string; siteId: string }) {
  const query = useSite(projectId, siteId);
  const deployments = useSiteDeployments(projectId, siteId);
  const upload = useUploadSiteDeployment(projectId, siteId);
  const site = query.data?.site;
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  if (query.error) return <ErrorState error={query.error} retry={() => query.refetch()} />;
  if (!site) return <EmptyState title="Site not found" description="The site may have been removed or is outside this project." />;
  const columns: ColumnDef<SiteDeployment, unknown>[] = [{ accessorKey: "version", header: "Version", cell: ({ row }) => `v${row.original.version}` }, { accessorKey: "source", header: "Source" }, { accessorKey: "status", header: "Status", cell: ({ row }) => <StatusBadge status={row.original.status} /> }, { accessorKey: "build_status", header: "Build", cell: ({ row }) => <StatusBadge status={row.original.build_status} /> }, { accessorKey: "created_at", header: "Created", cell: ({ row }) => formatDate(row.original.created_at) }];
  return <><BackLink href={`${base}/sites`} label="Back to sites" /><PageHeader eyebrow="Site" title={site.name} description="Static site deployment boundary." actions={<label className="inline-flex h-9 cursor-pointer items-center gap-2 rounded-lg bg-cyan-300 px-3.5 text-sm font-medium text-slate-950 hover:bg-cyan-200"><FileUp className="size-4" /> Upload archive<input type="file" accept=".zip,.tar,.gz,.tgz" className="sr-only" onChange={(event) => { const file = event.target.files?.[0]; if (file) upload.mutate({ file }, { onSuccess: () => toast.success("Site deployment queued") }); }} /></label>} /><div className="mb-5 flex items-center gap-2"><StatusBadge status={site.status} /><span className="text-xs text-slate-600">{site.framework} · {formatBytes(site.artifact_used_bytes)} used</span></div><Card><CardHeader><CardTitle>Deployments</CardTitle></CardHeader><DataTable data={deployments.data?.deployments ?? []} columns={columns} loading={deployments.isLoading} empty="No site deployments yet." /></Card></>;
}

export function DatabaseDetailView({ organizationId, projectId, databaseId }: { organizationId: string; projectId: string; databaseId: string }) {
  const query = useDatabase(projectId, databaseId);
  const tables = useDatabaseTables(projectId, databaseId);
  const database = query.data?.database;
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  if (query.error) return <ErrorState error={query.error} retry={() => query.refetch()} />;
  if (!database) return <EmptyState title="Database not found" description="The database may have been removed or is outside this project." />;
  const columns: ColumnDef<DatabaseTable, unknown>[] = [{ accessorKey: "name", header: "Table", cell: ({ row }) => <Link href={`${base}/databases/${databaseId}/tables/${row.original.id}`} className="font-medium text-white hover:text-amber-200">{row.original.name}</Link> }, { accessorKey: "row_security", header: "Row security", cell: ({ row }) => <StatusBadge status={row.original.row_security ? "active" : "inactive"} /> }, { accessorKey: "updated_at", header: "Updated", cell: ({ row }) => formatDate(row.original.updated_at) }];
  return <><BackLink href={`${base}/databases`} label="Back to databases" /><PageHeader eyebrow="Database" title={database.name} description="Schema browsing and row operations backed by the database API." /><Card><CardHeader><CardTitle className="flex items-center gap-2"><Table2 className="size-4 text-amber-300" /> Tables</CardTitle></CardHeader><DataTable data={tables.data?.tables ?? []} columns={columns} loading={tables.isLoading} empty="No tables yet." /></Card></>;
}

export function DatabaseRowsView({ organizationId, projectId, databaseId, tableId }: { organizationId: string; projectId: string; databaseId: string; tableId: string }) {
  const rows = useDatabaseRows(projectId, databaseId, tableId);
  const data = rows.data?.rows ?? [];
  const keys = Array.from(new Set(data.flatMap((row) => Object.keys(row.data))));
  return <><BackLink href={`/organizations/${organizationId}/projects/${projectId}/databases/${databaseId}`} label="Back to database" /><PageHeader eyebrow="Rows" title="Table data" description="Typed rows returned by the backend. Search and filtering follow the API's indexed query capabilities." /><Card><div className="border-b border-stealth-border p-4"><p className="font-mono text-xs text-slate-500">table {tableId}</p></div><DataTable data={data} columns={[{ accessorKey: "id", header: "ID", cell: ({ row }) => <span className="font-mono text-xs text-slate-500">{row.original.id.slice(0, 12)}…</span> }, ...keys.map((key): ColumnDef<DatabaseRow, unknown> => ({ id: key, header: key, cell: ({ row }) => <span className="max-w-xs truncate text-xs text-slate-300">{String(row.original.data[key] ?? "—")}</span> })), { accessorKey: "updated_at", header: "Updated", cell: ({ row }) => formatDate(row.original.updated_at) }]} loading={rows.isLoading} empty="No rows returned." /></Card></>;
}

export function BucketDetailView({ organizationId, projectId, bucketId }: { organizationId: string; projectId: string; bucketId: string }) {
  const bucket = useStorageBucket(projectId, bucketId);
  const files = useStorageFiles(projectId, bucketId);
  const [uploading, setUploading] = useState(false);
  const current = bucket.data?.bucket;
  const upload = async (file: File) => { setUploading(true); try { const form = new FormData(); form.append("file", file); const response = await fetch(`${process.env.NEXT_PUBLIC_API_BASE_URL ?? ""}/v1/projects/${projectId}/storage/buckets/${bucketId}/files`, { method: "POST", body: form, credentials: "include" }); if (!response.ok) throw new Error("File upload failed"); toast.success("File uploaded"); await files.refetch(); } catch (error) { toast.error(error instanceof Error ? error.message : "Upload failed"); } finally { setUploading(false); } };
  if (bucket.error) return <ErrorState error={bucket.error} retry={() => bucket.refetch()} />;
  if (!current) return <EmptyState title="Bucket not found" description="The bucket may have been removed or is outside this project." />;
  const columns: ColumnDef<StorageFile, unknown>[] = [{ accessorKey: "name", header: "Name", cell: ({ row }) => <span className="font-medium text-white">{row.original.name}</span> }, { accessorKey: "mime_type", header: "Type", cell: ({ row }) => <span className="text-xs text-slate-400">{row.original.mime_type}</span> }, { accessorKey: "size_bytes", header: "Size", cell: ({ row }) => formatBytes(row.original.size_bytes) }, { accessorKey: "updated_at", header: "Modified", cell: ({ row }) => formatDate(row.original.updated_at) }];
  return <><BackLink href={`/organizations/${organizationId}/projects/${projectId}/storage`} label="Back to storage" /><PageHeader eyebrow="Storage bucket" title={current.name} description="A flat object explorer backed by the storage files API." actions={<label className="inline-flex h-9 cursor-pointer items-center gap-2 rounded-lg bg-cyan-300 px-3.5 text-sm font-medium text-slate-950 hover:bg-cyan-200"><FileUp className="size-4" /> {uploading ? "Uploading…" : "Upload file"}<input type="file" className="sr-only" disabled={uploading} onChange={(event) => { const file = event.target.files?.[0]; if (file) void upload(file); }} /></label>} /><Card><CardHeader><CardTitle className="flex items-center gap-2"><HardDrive className="size-4 text-emerald-300" /> Files</CardTitle></CardHeader><DataTable data={files.data?.files ?? []} columns={columns} loading={files.isLoading} empty="No files in this bucket." /></Card></>;
}

export function WebhookDetailView({ organizationId, projectId, webhookId }: { organizationId: string; projectId: string; webhookId: string }) {
  const query = useWebhookDeliveries(projectId, webhookId);
  const columns: ColumnDef<WebhookDelivery, unknown>[] = [{ accessorKey: "event_name", header: "Event", cell: ({ row }) => <span className="font-mono text-xs text-white">{row.original.event_name}</span> }, { accessorKey: "status", header: "Status", cell: ({ row }) => <StatusBadge status={row.original.status} /> }, { accessorKey: "last_status_code", header: "HTTP", cell: ({ row }) => row.original.last_status_code ?? "—" }, { accessorKey: "attempt_count", header: "Attempts" }, { accessorKey: "created_at", header: "Created", cell: ({ row }) => formatDate(row.original.created_at) }];
  return <><BackLink href={`/organizations/${organizationId}/projects/${projectId}/webhooks`} label="Back to webhooks" /><PageHeader eyebrow="Webhook deliveries" title="Delivery history" description="The backend returns bounded delivery metadata. Request and response bodies are not exposed by this contract." /><Card><DataTable data={query.data?.deliveries ?? []} columns={columns} loading={query.isLoading} empty="No deliveries returned." /></Card></>;
}

export function AgentRunsView({ organizationId, projectId, agentId }: { organizationId: string; projectId: string; agentId: string }) {
  const runs = useAgentRuns(agentId);
  const create = useCreateAgentRun(agentId);
  const [prompt, setPrompt] = useState("");
  const columns: ColumnDef<AgentRun, unknown>[] = [{ accessorKey: "id", header: "Run", cell: ({ row }) => <span className="font-mono text-xs text-slate-400">{row.original.id.slice(0, 12)}…</span> }, { accessorKey: "status", header: "Status", cell: ({ row }) => <StatusBadge status={row.original.status} /> }, { accessorKey: "prompt", header: "Prompt", cell: ({ row }) => <span className="block max-w-lg truncate text-slate-300">{row.original.prompt}</span> }, { accessorKey: "created_at", header: "Created", cell: ({ row }) => formatDate(row.original.created_at) }];
  return <><BackLink href={`/organizations/${organizationId}/projects/${projectId}/agents`} label="Back to agents" /><PageHeader eyebrow="Agent runs" title="Task runner" description="Runs are durable queued tasks, not chat sessions. The catalog determines whether provider execution is ready." /><Card className="mb-5"><CardHeader><CardTitle>Queue a run</CardTitle></CardHeader><CardContent><textarea className="min-h-24 w-full rounded-lg border border-stealth-border bg-black/20 p-3 text-sm text-white placeholder:text-slate-600" value={prompt} onChange={(event) => setPrompt(event.target.value)} placeholder="Describe the task for this agent…" /><Button className="mt-3" disabled={!prompt.trim() || create.isPending} onClick={() => create.mutate({ prompt }, { onSuccess: () => { setPrompt(""); toast.success("Run queued"); } })}><Play className="size-4" /> Queue run</Button></CardContent></Card><Card><DataTable data={runs.data?.runs ?? []} columns={columns} loading={runs.isLoading} empty="No runs yet." /></Card></>;
}
