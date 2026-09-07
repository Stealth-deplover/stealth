"use client";

import { Check, ChevronsUpDown, FolderKanban, Plus, Search, Sparkles } from "lucide-react";
import { useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { useOrganization, useOrganizations, useProject, useProjects } from "@/api/queries";
import type { Organization, Project } from "@/api/types";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { cn, getInitials } from "@/lib/utils";

function SelectorItem({ icon, title, subtitle, selected, onClick }: { icon: React.ReactNode; title: string; subtitle?: string; selected?: boolean; onClick: () => void }) {
  return <button type="button" onClick={onClick} aria-current={selected ? "true" : undefined} className={cn("flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left transition hover:bg-white/[0.06]", selected && "bg-cyan-300/10")}>
    <span className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-white/10 bg-black/20 text-xs text-cyan-200">{icon}</span>
    <span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium text-white">{title}</span>{subtitle ? <span className="block truncate text-xs text-slate-500">{subtitle}</span> : null}</span>
    {selected ? <Check className="size-4 text-cyan-300" aria-label="Selected" /> : null}
  </button>;
}

export function OrganizationSwitcher({ currentId }: { currentId?: string }) {
  const router = useRouter();
  const { data, isLoading } = useOrganizations();
  const selectedOrganization = useOrganization(currentId);
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const organizations = useMemo(() => { const items = data?.organizations ?? []; const selected = selectedOrganization.data; return selected && !items.some((organization) => organization.id === selected.id) ? [selected, ...items] : items; }, [data?.organizations, selectedOrganization.data]);
  const filtered = useMemo(() => (organizations ?? []).filter((organization) => `${organization.name} ${organization.slug}`.toLowerCase().includes(search.toLowerCase())), [organizations, search]);
  const current = (organizations ?? []).find((organization) => organization.id === currentId);
  const navigate = (organization: Organization) => {
    setOpen(false);
    setSearch("");
    router.push(`/organizations/${organization.id}/projects`);
  };
  return <Dialog open={open} onOpenChange={setOpen}>
    <DialogTrigger asChild><Button variant="ghost" className="h-10 max-w-[7.5rem] justify-start px-2.5 sm:max-w-52"><span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-cyan-300/10 text-[10px] font-semibold text-cyan-200">{getInitials(current?.name ?? "S")}</span><span className="min-w-0 flex-1 text-left"><span className="block truncate text-xs font-semibold text-white">{current?.name ?? "Organizations"}</span><span className="block truncate text-[10px] text-slate-500">Workspace</span></span><ChevronsUpDown className="size-3.5 shrink-0 text-slate-500" /></Button></DialogTrigger>
    <DialogContent className="max-w-md"><DialogHeader><DialogTitle>Choose organization</DialogTitle><DialogDescription>Switch the workspace context for this console.</DialogDescription></DialogHeader><div className="relative mb-3"><Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-slate-600" /><Input autoFocus value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search organizations" className="pl-9" /></div><div className="max-h-72 space-y-1 overflow-y-auto">{isLoading ? <p className="px-3 py-4 text-sm text-slate-500">Loading organizations…</p> : filtered.length ? filtered.map((organization) => <SelectorItem key={organization.id} icon={getInitials(organization.name)} title={organization.name} subtitle={organization.slug} selected={organization.id === currentId} onClick={() => navigate(organization)} />) : <p className="px-3 py-4 text-sm text-slate-500">No matching organizations.</p>}</div><Button variant="outline" className="mt-4 w-full" onClick={() => { setOpen(false); router.push("/organizations"); }}><Plus className="size-4" /> Create organization</Button></DialogContent>
  </Dialog>;
}

export function ProjectSwitcher({ organizationId, currentId }: { organizationId?: string; currentId?: string }) {
  const router = useRouter();
  const { data } = useProjects(organizationId);
  const selectedProject = useProject(currentId);
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const projects = useMemo(() => { const items = data?.projects ?? []; const selected = selectedProject.data?.project; return selected && selected.organization_id === organizationId && !items.some((project) => project.id === selected.id) ? [selected, ...items] : items; }, [data?.projects, organizationId, selectedProject.data?.project]);
  const filtered = useMemo(() => (projects ?? []).filter((project) => project.name.toLowerCase().includes(search.toLowerCase())), [projects, search]);
  const current = (projects ?? []).find((project) => project.id === currentId);
  const navigate = (project: Project) => { setOpen(false); setSearch(""); router.push(`/organizations/${project.organization_id}/projects/${project.id}`); };
  return <Dialog open={open} onOpenChange={setOpen}>
    <DialogTrigger asChild><Button variant="ghost" className="h-10 max-w-[7.5rem] justify-start border-l border-stealth-border pl-3 pr-2.5 sm:max-w-60"><span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-violet-300/10 text-violet-200"><FolderKanban className="size-3.5" /></span><span className="min-w-0 flex-1 text-left"><span className="block truncate text-xs font-semibold text-white">{current?.name ?? "Select project"}</span><span className="block truncate text-[10px] text-slate-500">Project</span></span><ChevronsUpDown className="size-3.5 shrink-0 text-slate-500" /></Button></DialogTrigger>
    <DialogContent className="max-w-md"><DialogHeader><DialogTitle>Choose project</DialogTitle><DialogDescription>Projects are scoped to the selected organization.</DialogDescription></DialogHeader><div className="relative mb-3"><Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-slate-600" /><Input autoFocus value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search projects" className="pl-9" /></div><div className="max-h-72 space-y-1 overflow-y-auto">{filtered.length ? filtered.map((project) => <SelectorItem key={project.id} icon={<FolderKanban className="size-3.5" />} title={project.name} subtitle={`Created ${new Date(project.created_at).toLocaleDateString()}`} selected={project.id === currentId} onClick={() => navigate(project)} />) : <p className="px-3 py-4 text-sm text-slate-500">No projects in this organization yet.</p>}</div><Button variant="outline" className="mt-4 w-full" onClick={() => { setOpen(false); router.push(`/organizations/${organizationId}/projects`); }}><Plus className="size-4" /> Create project</Button></DialogContent>
  </Dialog>;
}

export function ContextBadge({ projectId }: { projectId?: string }) {
  return projectId ? <span className="hidden items-center gap-1.5 text-[11px] text-slate-500 md:flex"><Sparkles className="size-3 text-cyan-300" /> Live API context</span> : null;
}
