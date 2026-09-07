"use client";

import Link from "next/link";
import type { ColumnDef } from "@tanstack/react-table";
import { Activity, ArrowUpRight, FolderKanban, Mail, Users } from "lucide-react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { useCreateOrganization, useCreateProject } from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { useMemberships, useOrganization, useOrganizationAudit, useOrganizationPlan, useOrganizationTraces, useOrganizations, useProjects } from "@/api/queries";
import type { Membership, Project } from "@/api/types";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { CursorPaginationControls } from "@/components/cursor-pagination-controls";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { formatCount, formatDate } from "@/lib/format";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";

function pageControls(navigation: ReturnType<typeof useCursorPagination>, next: string | null, isFetching: boolean) {
  return { canPrevious: navigation.canPrevious, canNext: Boolean(next), onPrevious: navigation.goPrevious, onNext: () => navigation.goNext(next), isFetching };
}

function ProjectList({ organizationId }: { organizationId: string }) {
  const router = useRouter();
  const navigation = useCursorPagination();
  const query = useProjects(organizationId, { cursor: navigation.cursor });
  const create = useCreateProject(organizationId);
  const projects = query.data?.projects ?? [];
  if (query.isError) return <ErrorState error={query.error} retry={() => query.refetch()} />;
  return <div className="space-y-4"><div className="flex items-center justify-between"><div><h2 className="text-sm font-semibold text-white">Projects</h2><p className="mt-1 text-xs text-slate-500">Each project maps to one isolated Stealth application boundary.</p></div><CreateDialog label="Project" title="Create a project" description="Project names are normalized to stable API slugs, for example Production API becomes production-api." fields={[{ name: "name", label: "Project name", placeholder: "Production API", help: "Use a readable name; the console sends the lowercase hyphenated slug required by the API." }]} pending={create.isPending} onSubmit={async (values) => { await create.mutateAsync({ name: values.name }); toast.success("Project created"); }} /></div>{query.isLoading ? <DataTable columns={[{ accessorKey: "name", header: "Name" }]} data={[]} loading /> : projects.length || navigation.canPrevious ? <Card><DataTable<Project> data={projects} serverPagination={pageControls(navigation, nextCursor(query.data), query.isFetching)} empty="No projects returned on this page." columns={[{ accessorKey: "name", header: "Project", cell: ({ row }) => <button type="button" className="font-medium text-white hover:text-cyan-200" onClick={() => router.push(`/organizations/${organizationId}/projects/${row.original.id}`)}>{row.original.name}</button> }, { accessorKey: "id", header: "Project ID", cell: ({ row }) => <span className="font-mono text-xs text-slate-500">{row.original.id.slice(0, 8)}…</span> }, { accessorKey: "created_at", header: "Created", cell: ({ row }) => formatDate(row.original.created_at) }, { id: "action", header: "", cell: ({ row }) => <Button asChild variant="ghost" size="sm"><Link href={`/organizations/${organizationId}/projects/${row.original.id}`}>Open <ArrowUpRight className="size-3.5" /></Link></Button> }]} /></Card> : <EmptyState title="No projects yet" description="Create a project to get a resource-aware developer console and a clean API boundary." actionLabel="Create project" action={() => document.querySelector<HTMLButtonElement>("[data-create-dialog]")?.click()} />}</div>;
}

export function OrganizationProjectsView({ organizationId }: { organizationId: string }) { return <><PageHeader eyebrow="Organization" title="Projects" description="Your project's control plane starts here." /><ProjectList organizationId={organizationId} /></>; }

export function OrganizationsIndexView() {
  const router = useRouter();
  const navigation = useCursorPagination();
  const query = useOrganizations({ cursor: navigation.cursor });
  const create = useCreateOrganization();
  const organizations = query.data?.organizations ?? [];
  return <><PageHeader eyebrow="Account" title="Organizations" description="Choose a workspace, then open a project to operate its services." actions={<CreateDialog label="Organization" title="Create an organization" description="Organizations group people, projects, and plan limits." fields={[{ name: "name", label: "Display name", placeholder: "Acme Inc" }, { name: "slug", label: "Slug", placeholder: "acme-inc", help: "Lowercase letters, numbers, and hyphens." }]} pending={create.isPending} onSubmit={async (values) => { await create.mutateAsync({ name: values.name, slug: values.slug }); toast.success("Organization created"); }} />} />{query.isError ? <ErrorState error={query.error} retry={() => query.refetch()} /> : organizations.length || navigation.canPrevious ? <><div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">{organizations.map((organization) => <Card key={organization.id} className="group transition hover:border-cyan-300/30"><CardContent className="p-5"><div className="flex items-start justify-between"><span className="flex size-10 items-center justify-center rounded-xl border border-cyan-300/20 bg-cyan-300/10 text-sm font-semibold text-cyan-200">{organization.name.slice(0, 1).toUpperCase()}</span><Badge variant="neutral">Workspace</Badge></div><h2 className="mt-5 text-lg font-semibold text-white">{organization.name}</h2><p className="mt-1 font-mono text-xs text-slate-600">{organization.slug}</p><p className="mt-5 text-xs text-slate-500">Created {formatDate(organization.created_at)}</p><Button className="mt-5 w-full" variant="outline" onClick={() => router.push(`/organizations/${organization.id}/projects`)}>Open workspace <ArrowUpRight className="size-3.5" /></Button></CardContent></Card>)}</div><CursorPaginationControls {...pageControls(navigation, nextCursor(query.data), query.isFetching)} label={`${organizations.length} organizations on this page`} /></> : <EmptyState title="No organizations yet" description="Create a workspace to start mapping projects to the Stealth API." />}</>;
}

export function OrganizationOverviewView({ organizationId }: { organizationId: string }) {
  const organization = useOrganization(organizationId);
  const plan = useOrganizationPlan(organizationId);
  const current = organization.data;
  return <><PageHeader eyebrow="Organization" title={current?.name ?? "Organization"} description="A clear view of your teams, projects, and operating limits." actions={<Button asChild variant="outline"><Link href={`/organizations/${organizationId}/projects`}><FolderKanban className="size-4" /> Open projects</Link></Button>} /><div className="grid gap-4 md:grid-cols-3"><Card><CardContent className="p-5"><div className="flex items-center justify-between"><p className="text-xs uppercase tracking-[0.14em] text-slate-600">Projects</p><FolderKanban className="size-4 text-cyan-300" /></div><p className="mt-3 text-3xl font-semibold text-white">{formatCount(plan.data?.plan.usage.projects)}</p></CardContent></Card><Card><CardContent className="p-5"><div className="flex items-center justify-between"><p className="text-xs uppercase tracking-[0.14em] text-slate-600">Plan</p><Activity className="size-4 text-violet-300" /></div><p className="mt-3 text-3xl font-semibold capitalize text-white">{plan.data?.plan.plan_key ?? "—"}</p></CardContent></Card><Card><CardContent className="p-5"><div className="flex items-center justify-between"><p className="text-xs uppercase tracking-[0.14em] text-slate-600">Members</p><Users className="size-4 text-amber-300" /></div><p className="mt-3 text-3xl font-semibold text-white">{formatCount(plan.data?.plan.usage.members)}</p></CardContent></Card></div><div className="mt-8"><ProjectList organizationId={organizationId} /></div></>;
}

export function OrganizationMembersView({ organizationId }: { organizationId: string }) {
  const navigation = useCursorPagination();
  const query = useMemberships(organizationId, { cursor: navigation.cursor });
  const columns: ColumnDef<Membership, unknown>[] = [{ accessorKey: "email", header: "Identity", cell: ({ row }) => <div><p className="font-medium text-white">{row.original.email}</p><p className="font-mono text-[10px] text-slate-600">{row.original.account_id}</p></div> }, { accessorKey: "role", header: "Role", cell: ({ row }) => <Badge variant={row.original.role === "owner" ? "default" : "neutral"}>{row.original.role}</Badge> }, { accessorKey: "created_at", header: "Joined", cell: ({ row }) => formatDate(row.original.created_at) }];
  return <><PageHeader eyebrow="Organization" title="Members" description="Membership and roles are enforced by the Go API." actions={<Button variant="outline"><Mail className="size-4" /> Invite member</Button>} />{query.isError ? <ErrorState error={query.error} retry={() => query.refetch()} /> : <Card><DataTable columns={columns} data={query.data?.memberships ?? []} loading={query.isLoading} empty="No memberships returned." serverPagination={pageControls(navigation, nextCursor(query.data), query.isFetching)} /></Card>}</>;
}

export function OrganizationPlanView({ organizationId }: { organizationId: string }) {
  const query = useOrganizationPlan(organizationId);
  const plan = query.data?.plan;
  if (query.isError) return <ErrorState error={query.error} retry={() => query.refetch()} />;
  return <><PageHeader eyebrow="Organization" title="Plan & limits" description="Read-only plan data from the backend. Billing workflows are not exposed by the current API." />{plan ? <div className="grid gap-4 lg:grid-cols-[1fr_1fr]"><Card><CardHeader><CardTitle className="flex items-center justify-between"><span className="capitalize">{plan.plan_key} plan</span><StatusBadge status={plan.status} /></CardTitle></CardHeader><CardContent><div className="grid grid-cols-2 gap-3">{Object.entries(plan.limits).map(([key, limit]) => <div key={key} className="rounded-lg border border-stealth-border bg-black/10 p-3"><p className="text-xs capitalize text-slate-500">{key.replace(/_/g, " ")}</p><p className="mt-1 text-lg font-semibold text-white">{limit === -1 ? "Unlimited" : limit}</p></div>)}</div></CardContent></Card><Card><CardHeader><CardTitle>Current usage</CardTitle></CardHeader><CardContent><div className="grid grid-cols-2 gap-3">{Object.entries(plan.usage).map(([key, value]) => <div key={key} className="rounded-lg border border-stealth-border bg-black/10 p-3"><p className="text-xs capitalize text-slate-500">{key.replace(/_/g, " ")}</p><p className="mt-1 text-lg font-semibold text-white">{value}</p></div>)}</div></CardContent></Card></div> : <EmptyState title="Plan unavailable" description="The API did not return plan data for this organization." />}</>;
}

export function OrganizationAuditView({ organizationId }: { organizationId: string }) {
  const navigation = useCursorPagination();
  const query = useOrganizationAudit(organizationId, { cursor: navigation.cursor });
  return <><PageHeader eyebrow="Organization" title="Audit" description="Durable activity events emitted by the platform." />{query.isError ? <ErrorState error={query.error} retry={() => query.refetch()} /> : <Card><DataTable data={query.data?.events ?? []} loading={query.isLoading} columns={[{ accessorKey: "action", header: "Action", cell: ({ row }) => <div><p className="font-medium text-white">{row.original.action}</p><p className="text-xs text-slate-500">{row.original.target_type}</p></div> }, { accessorKey: "actor_email", header: "Actor", cell: ({ row }) => row.original.actor_email ?? "System" }, { accessorKey: "created_at", header: "When", cell: ({ row }) => formatDate(row.original.created_at) }]} serverPagination={pageControls(navigation, nextCursor(query.data), query.isFetching)} /></Card>}</>;
}

export function OrganizationIncidentsView({ organizationId }: { organizationId: string }) {
  return <><PageHeader eyebrow="Organization" title="Incidents" description="Operational incident workflows are available from the backend; this surface stays explicit about the current incident data boundary." /><Card><CardContent className="p-7"><div className="flex items-start gap-3"><Activity className="mt-0.5 size-5 text-amber-300" /><div><h2 className="text-sm font-semibold text-white">Incident timeline</h2><p className="mt-1 max-w-xl text-sm leading-6 text-slate-500">Use the organization incident endpoints for durable incidents and timeline updates. The project console keeps this view separate from runtime health metrics.</p><code className="mt-4 block rounded-lg border border-stealth-border bg-black/20 px-3 py-2 font-mono text-xs text-slate-400">/v1/organizations/{organizationId}/incidents</code></div></div></CardContent></Card></>;
}

export function OrganizationTracesView({ organizationId }: { organizationId: string }) {
  const navigation = useCursorPagination();
  const query = useOrganizationTraces(organizationId, { cursor: navigation.cursor });
  return <><PageHeader eyebrow="Organization" title="Traces" description="Durable root HTTP request traces. The backend does not currently expose span hierarchy or server-side status filters." />{query.isError ? <ErrorState error={query.error} retry={() => query.refetch()} /> : <Card><DataTable data={query.data?.traces ?? []} columns={[{ accessorKey: "started_at", header: "Timestamp", cell: ({ row }) => formatDate(row.original.started_at) }, { accessorKey: "service", header: "Service" }, { accessorKey: "method", header: "Method", cell: ({ row }) => <span className="font-mono text-xs text-cyan-200">{row.original.method}</span> }, { accessorKey: "status", header: "Status", cell: ({ row }) => <StatusBadge status={row.original.status >= 500 ? "error" : row.original.status >= 400 ? "warning" : "success"} /> }, { accessorKey: "duration_ms", header: "Duration", cell: ({ row }) => `${row.original.duration_ms} ms` }, { accessorKey: "trace_id", header: "Trace ID", cell: ({ row }) => <span className="font-mono text-xs text-slate-500">{row.original.trace_id}</span> }]} loading={query.isLoading} empty="No traces returned." serverPagination={pageControls(navigation, nextCursor(query.data), query.isFetching)} /></Card>}</>;
}
