"use client";

import { Command, Search } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";

export function CommandPalette() {
  const router = useRouter();
  const pathname = usePathname();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const segments = pathname.split("/").filter(Boolean);
  const organizationId = segments[0] === "organizations" ? segments[1] : undefined;
  const projectId = organizationId && segments[2] === "projects" ? segments[3] : undefined;
  const base = projectId ? `/organizations/${organizationId}/projects/${projectId}` : organizationId ? `/organizations/${organizationId}` : "/organizations";
  const commands = useMemo(() => [
    { label: "Project overview", hint: "View current project", href: base, enabled: Boolean(projectId) },
    { label: "Services canvas", hint: "Map supported resources", href: `${base}/services`, enabled: Boolean(projectId) },
    { label: "Functions", hint: "Run backend code", href: `${base}/functions`, enabled: Boolean(projectId) },
    { label: "Databases", hint: "Browse schema and rows", href: `${base}/databases`, enabled: Boolean(projectId) },
    { label: "Storage", hint: "Explore buckets and files", href: `${base}/storage`, enabled: Boolean(projectId) },
    { label: "Logs", hint: "Open contextual log viewer", href: `${base}/observability/logs`, enabled: Boolean(projectId) },
    { label: "Traces", hint: "Inspect durable root requests", href: `${base}/observability/traces`, enabled: Boolean(projectId) },
    { label: "Project settings", hint: "Auth and configuration", href: `${base}/settings/project`, enabled: Boolean(projectId) },
  ].filter((command) => command.enabled && `${command.label} ${command.hint}`.toLowerCase().includes(query.toLowerCase())), [base, projectId, query]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => { if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") { event.preventDefault(); setOpen(true); } };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  return <Dialog open={open} onOpenChange={(value) => { setOpen(value); if (!value) setQuery(""); }}>
    <DialogContent className="max-w-xl p-3"><DialogHeader className="sr-only"><DialogTitle>Command palette</DialogTitle><DialogDescription>Navigate through the console.</DialogDescription></DialogHeader><div className="relative"><Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-slate-600" /><Input autoFocus value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search the console…" className="h-11 border-transparent bg-black/20 pl-9 text-sm focus:border-cyan-300/40" /></div><div className="mt-2 max-h-80 overflow-y-auto">{commands.length ? commands.map((command) => <button type="button" key={command.href} onClick={() => { setOpen(false); router.push(command.href); }} className="flex w-full items-center gap-3 rounded-xl px-3 py-3 text-left hover:bg-white/[0.06]"><span className="flex size-8 items-center justify-center rounded-lg border border-white/10 bg-white/[0.03] text-slate-400"><Command className="size-4" /></span><span className="flex-1"><span className="block text-sm font-medium text-slate-100">{command.label}</span><span className="block text-xs text-slate-500">{command.hint}</span></span><span className="text-[10px] text-slate-600">↵</span></button>) : <p className="px-3 py-8 text-center text-sm text-slate-500">No matching command.</p>}</div><div className="mt-2 flex items-center justify-between border-t border-stealth-border px-3 pt-3 text-[10px] text-slate-600"><span>Jump to a resource</span><span className="rounded border border-stealth-border px-1.5 py-0.5">Esc</span></div></DialogContent>
  </Dialog>;
}
