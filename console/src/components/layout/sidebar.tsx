"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  Activity,
  Bot,
  Boxes,
  Cable,
  CloudCog,
  Database,
  FolderKanban,
  Gauge,
  Globe2,
  KeyRound,
  Layers3,
  MessageSquare,
  PanelLeftClose,
  PanelLeftOpen,
  Settings2,
  ShieldCheck,
  Users,
  Webhook,
  Zap,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";

type NavItem = {
  label: string;
  href: string;
  icon: LucideIcon;
  exact?: boolean;
};

function NavGroup({
  label,
  items,
  collapsed,
}: {
  label: string;
  items: NavItem[];
  collapsed: boolean;
}) {
  const pathname = usePathname();
  return (
    <div className="mb-5">
      <p
        className={cn(
          "mb-2 px-3 text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-600",
          collapsed && "sr-only",
        )}
      >
        {label}
      </p>
      <nav className="space-y-0.5" aria-label={label}>
        {items.map((item) => {
          const active =
            pathname === item.href ||
            (!item.exact &&
              item.href !== "/" &&
              pathname.startsWith(`${item.href}/`));
          const Icon = item.icon;
          return (
            <Link
              key={item.href}
              href={item.href}
              title={collapsed ? item.label : undefined}
              aria-current={active ? "page" : undefined}
              className={cn(
                "group flex items-center gap-2.5 rounded-lg px-3 py-2 text-[13px] text-slate-500 transition hover:bg-white/[0.05] hover:text-slate-100",
                collapsed && "justify-center px-2",
                active && "bg-cyan-300/[0.09] text-cyan-200",
              )}
            >
              <Icon
                className={cn(
                  "size-4 shrink-0",
                  active
                    ? "text-cyan-300"
                    : "text-slate-600 group-hover:text-slate-300",
                )}
              />
              {collapsed ? (
                <span className="sr-only">{item.label}</span>
              ) : (
                <span className="min-w-0 truncate">{item.label}</span>
              )}
              {active && !collapsed ? (
                <span className="ml-auto size-1.5 rounded-full bg-cyan-300" />
              ) : null}
            </Link>
          );
        })}
      </nav>
    </div>
  );
}

export function Sidebar({
  organizationId,
  projectId,
  mobile = false,
  collapsed = false,
  onToggle,
}: {
  organizationId?: string;
  projectId?: string;
  mobile?: boolean;
  collapsed?: boolean;
  onToggle?: () => void;
}) {
  const orgBase = organizationId
    ? `/organizations/${organizationId}`
    : "/organizations";
  const projectBase =
    organizationId && projectId
      ? `${orgBase}/projects/${projectId}`
      : undefined;
  const orgItems: NavItem[] = organizationId
    ? [
        {
          label: "Projects",
          href: `${orgBase}/projects`,
          icon: FolderKanban,
          exact: true,
        },
        { label: "Members", href: `${orgBase}/members`, icon: Users },
        { label: "Plan & limits", href: `${orgBase}/plan`, icon: Gauge },
        { label: "Incidents", href: `${orgBase}/incidents`, icon: Activity },
        { label: "Audit", href: `${orgBase}/audit`, icon: ShieldCheck },
      ]
    : [
        {
          label: "Organizations",
          href: "/organizations",
          icon: FolderKanban,
          exact: true,
        },
      ];
  const projectItems: NavItem[] = projectBase
    ? [
        { label: "Overview", href: projectBase, icon: Gauge, exact: true },
        { label: "Services", href: `${projectBase}/services`, icon: Boxes },
        {
          label: "Deployments",
          href: `${projectBase}/deployments`,
          icon: CloudCog,
        },
      ]
    : [];
  const computeItems: NavItem[] = projectBase
    ? [
        { label: "Functions", href: `${projectBase}/functions`, icon: Zap },
        { label: "Sites", href: `${projectBase}/sites`, icon: Globe2 },
        { label: "Agents", href: `${projectBase}/agents`, icon: Bot },
      ]
    : [];
  const dataItems: NavItem[] = projectBase
    ? [
        {
          label: "Databases",
          href: `${projectBase}/databases`,
          icon: Database,
        },
        { label: "Storage", href: `${projectBase}/storage`, icon: Layers3 },
      ]
    : [];
  const platformItems: NavItem[] = projectBase
    ? [
        { label: "Users", href: `${projectBase}/users`, icon: Users },
        {
          label: "Messaging",
          href: `${projectBase}/messaging`,
          icon: MessageSquare,
        },
        { label: "Webhooks", href: `${projectBase}/webhooks`, icon: Webhook },
        { label: "API keys", href: `${projectBase}/api-keys`, icon: KeyRound },
      ]
    : [];
  const observability: NavItem[] = projectBase
    ? [
        {
          label: "Logs",
          href: `${projectBase}/observability/logs`,
          icon: Cable,
        },
        {
          label: "Traces",
          href: `${projectBase}/observability/traces`,
          icon: Activity,
        },
      ]
    : [];
  const settings: NavItem[] = projectBase
    ? [
        {
          label: "Project settings",
          href: `${projectBase}/settings/project`,
          icon: Settings2,
        },
        {
          label: "Auth settings",
          href: `${projectBase}/auth`,
          icon: ShieldCheck,
        },
      ]
    : [];
  return (
    <aside
      aria-label="Primary navigation"
      className={cn(
        "scrollbar-thin shrink-0 flex-col overflow-y-auto border-r border-stealth-border bg-stealth-panel/70 py-5 transition-[width] duration-200",
        mobile
          ? "flex w-72 px-3"
          : cn("hidden lg:flex", collapsed ? "w-[4.5rem] px-2" : "w-60 px-3"),
      )}
    >
      <div
        className={cn(
          "mb-8 flex items-center gap-2.5",
          collapsed && !mobile ? "justify-center" : "px-3",
        )}
      >
        <span className="flex size-8 shrink-0 items-center justify-center rounded-xl border border-cyan-300/30 bg-cyan-300/10 text-sm font-black text-cyan-200">
          S
        </span>
        {!collapsed || mobile ? (
          <div className="min-w-0">
            <p className="text-sm font-semibold tracking-tight text-white">
              Stealth
            </p>
            <p className="text-[10px] uppercase tracking-[0.2em] text-slate-600">
              Control plane
            </p>
          </div>
        ) : null}
        {onToggle && !mobile ? (
          <button
            type="button"
            onClick={onToggle}
            className="ml-auto rounded-md p-1.5 text-slate-600 transition hover:bg-white/[0.06] hover:text-slate-200"
            aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          >
            {collapsed ? (
              <PanelLeftOpen className="size-4" />
            ) : (
              <PanelLeftClose className="size-4" />
            )}
          </button>
        ) : null}
      </div>
      <NavGroup
        label={projectBase ? "Workspace" : "Organization"}
        items={orgItems}
        collapsed={collapsed && !mobile}
      />
      {projectBase ? (
        <>
          <NavGroup
            label="Project"
            items={projectItems}
            collapsed={collapsed && !mobile}
          />
          <NavGroup
            label="Compute"
            items={computeItems}
            collapsed={collapsed && !mobile}
          />
          <NavGroup
            label="Data"
            items={dataItems}
            collapsed={collapsed && !mobile}
          />
          <NavGroup
            label="Platform"
            items={platformItems}
            collapsed={collapsed && !mobile}
          />
          <NavGroup
            label="Observability"
            items={observability}
            collapsed={collapsed && !mobile}
          />
          <NavGroup
            label="System"
            items={settings}
            collapsed={collapsed && !mobile}
          />
        </>
      ) : (
        <div
          className={cn(
            "rounded-xl border border-dashed border-stealth-border px-3 py-4 text-xs leading-5 text-slate-600",
            collapsed && !mobile && "border-0 px-0 text-center text-[0px]",
          )}
        >
          {collapsed && !mobile ? (
            <span className="sr-only">
              Select a project to open its service console.
            </span>
          ) : (
            "Select a project to open its service console."
          )}
        </div>
      )}
      <div
        className={cn(
          "mt-auto pt-8 text-[10px] leading-5 text-slate-700",
          collapsed && !mobile ? "px-1 text-center" : "px-3",
        )}
      >
        {collapsed && !mobile ? (
          <span className="text-sm text-slate-700">·</span>
        ) : (
          <>
            API is the platform.
            <br />
            Console is the interface.
          </>
        )}
      </div>
    </aside>
  );
}
