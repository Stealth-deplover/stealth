"use client";

import Link from "next/link";
import { ChevronRight } from "lucide-react";
import { usePathname } from "next/navigation";
import { useOrganization, useProject } from "@/api/queries";
import { cn } from "@/lib/utils";

const resourceLabels: Record<string, string> = {
  services: "Services",
  deployments: "Deployments",
  functions: "Functions",
  sites: "Sites",
  databases: "Databases",
  storage: "Storage",
  users: "Users",
  messaging: "Messaging",
  webhooks: "Webhooks",
  "api-keys": "API keys",
  agents: "Agents",
  observability: "Observability",
  logs: "Logs",
  traces: "Traces",
  settings: "Settings",
  auth: "Auth settings",
};

function fallbackName(value: string | undefined, prefix: string) {
  if (!value) return prefix;
  return `${prefix} ${value.slice(0, 8)}`;
}

export function Breadcrumbs({ organizationId, projectId }: { organizationId?: string; projectId?: string }) {
  const pathname = usePathname();
  const organization = useOrganization(organizationId);
  const project = useProject(projectId);
  if (!organizationId) return null;

  const orgBase = `/organizations/${organizationId}`;
  const items: Array<{ label: string; href?: string }> = [{ label: organization.data?.name ?? fallbackName(organizationId, "Organization"), href: `${orgBase}/projects` }];
  if (!projectId) {
    items.push({ label: pathname.includes("/projects") ? "Projects" : "Workspace" });
  } else {
    const projectName = project.data?.project?.name ?? fallbackName(projectId, "Project");
    const projectBase = `${orgBase}/projects/${projectId}`;
    items.push({ label: projectName, href: projectBase });
    const rest = pathname.slice(projectBase.length).split("/").filter(Boolean);
    if (rest[0]) {
      const resource = resourceLabels[rest[0]] ?? rest[0].replace(/-/g, " ");
      items.push({ label: resource });
    }
  }

  return <nav aria-label="Breadcrumb" className="hidden min-w-0 items-center gap-1 text-xs text-slate-500 xl:flex">
    {items.map((item, index) => <span key={`${item.label}-${index}`} className="inline-flex min-w-0 items-center gap-1">
      {index > 0 ? <ChevronRight className="size-3 shrink-0 text-slate-700" /> : null}
      {item.href && index < items.length - 1 ? <Link href={item.href} className={cn("max-w-36 truncate transition hover:text-slate-200", index === 0 && "max-w-28")}>{item.label}</Link> : <span className="max-w-40 truncate text-slate-300">{item.label}</span>}
    </span>)}
  </nav>;
}
