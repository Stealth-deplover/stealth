"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Activity, Bot, Boxes, Cable, CloudCog, Database, FolderKanban, Gauge, Globe2, KeyRound, Layers3, MessageSquare, Settings2, ShieldCheck, Users, Webhook, Zap } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";

type NavItem = { label: string; href: string; icon: LucideIcon };

function NavGroup({ label, items }: { label: string; items: NavItem[] }) {
  const pathname = usePathname();
  return <div className="mb-6"><p className="mb-2 px-3 text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-600">{label}</p><nav className="space-y-0.5">{items.map((item) => { const active = pathname === item.href || (item.href !== "/" && pathname.startsWith(`${item.href}/`)); const Icon = item.icon; return <Link key={item.href} href={item.href} className={cn("group flex items-center gap-2.5 rounded-lg px-3 py-2 text-[13px] text-slate-500 transition hover:bg-white/[0.05] hover:text-slate-100", active && "bg-cyan-300/[0.09] text-cyan-200") }><Icon className={cn("size-4 shrink-0", active ? "text-cyan-300" : "text-slate-600 group-hover:text-slate-300")} />{item.label}{active ? <span className="ml-auto size-1.5 rounded-full bg-cyan-300" /> : null}</Link>; })}</nav></div>;
}

export function Sidebar({ organizationId, projectId }: { organizationId?: string; projectId?: string }) {
  const orgBase = organizationId ? `/organizations/${organizationId}` : "/organizations";
  const projectBase = organizationId && projectId ? `${orgBase}/projects/${projectId}` : undefined;
  const orgItems: NavItem[] = [{ label: "Projects", href: `${orgBase}/projects`, icon: FolderKanban }, { label: "Members", href: `${orgBase}/members`, icon: Users }, { label: "Plan & limits", href: `${orgBase}/plan`, icon: Gauge }, { label: "Incidents", href: `${orgBase}/incidents`, icon: Activity }, { label: "Audit", href: `${orgBase}/audit`, icon: ShieldCheck }];
  const projectItems: NavItem[] = projectBase ? [
    { label: "Overview", href: projectBase, icon: Gauge },
    { label: "Services", href: `${projectBase}/services`, icon: Boxes },
    { label: "Deployments", href: `${projectBase}/deployments`, icon: CloudCog },
    { label: "Functions", href: `${projectBase}/functions`, icon: Zap },
    { label: "Sites", href: `${projectBase}/sites`, icon: Globe2 },
    { label: "Databases", href: `${projectBase}/databases`, icon: Database },
    { label: "Storage", href: `${projectBase}/storage`, icon: Layers3 },
    { label: "Users", href: `${projectBase}/users`, icon: Users },
    { label: "Messaging", href: `${projectBase}/messaging`, icon: MessageSquare },
    { label: "Webhooks", href: `${projectBase}/webhooks`, icon: Webhook },
    { label: "API keys", href: `${projectBase}/api-keys`, icon: KeyRound },
    { label: "Agents", href: `${projectBase}/agents`, icon: Bot },
  ] : [];
  const observability: NavItem[] = projectBase ? [{ label: "Logs", href: `${projectBase}/observability/logs`, icon: Cable }, { label: "Traces", href: `${projectBase}/observability/traces`, icon: Activity }] : [];
  const settings: NavItem[] = projectBase ? [{ label: "Project settings", href: `${projectBase}/settings/project`, icon: Settings2 }, { label: "Auth settings", href: `${projectBase}/auth`, icon: ShieldCheck }] : [];
  return <aside className="scrollbar-thin hidden w-60 shrink-0 flex-col overflow-y-auto border-r border-stealth-border bg-stealth-panel/70 px-3 py-5 lg:flex"><div className="mb-8 flex items-center gap-2.5 px-3"><span className="flex size-8 items-center justify-center rounded-xl border border-cyan-300/30 bg-cyan-300/10 text-sm font-black text-cyan-200">S</span><div><p className="text-sm font-semibold tracking-tight text-white">Stealth</p><p className="text-[10px] uppercase tracking-[0.2em] text-slate-600">Control plane</p></div></div><NavGroup label={projectBase ? "Workspace" : "Organization"} items={orgItems} />{projectBase ? <><NavGroup label="Project" items={projectItems} /><NavGroup label="Observability" items={observability} /><NavGroup label="Configure" items={settings} /></> : <div className="rounded-xl border border-dashed border-stealth-border px-3 py-4 text-xs leading-5 text-slate-600">Select a project to open its service console.</div>}<div className="mt-auto px-3 pt-8 text-[10px] leading-5 text-slate-700">API is the platform.<br />Console is the interface.</div></aside>;
}
