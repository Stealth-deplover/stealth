"use client";

import Link from "next/link";
import {
  Activity,
  ClipboardList,
  DatabaseZap,
  Gauge,
  BellRing,
  CircleAlert,
  LayoutDashboard,
  Logs,
  Radar,
  RadioTower,
  ServerCog,
  ShieldCheck,
  Waypoints,
} from "lucide-react";
import { usePathname } from "next/navigation";
import { cn } from "@/lib/utils";

const links = [
  { href: "/admin", label: "Overview", icon: Gauge },
  { href: "/admin/operations", label: "Operations", icon: ClipboardList },
  { href: "/admin/monitoring", label: "Monitoring", icon: Radar },
  { href: "/admin/alerts", label: "Alerts", icon: BellRing },
  { href: "/admin/incidents", label: "Incidents", icon: CircleAlert },
  { href: "/admin/dashboards", label: "Dashboards", icon: LayoutDashboard },
  {
    href: "/admin/infrastructure",
    label: "Infrastructure",
    icon: ServerCog,
  },
  { href: "/admin/telemetry/metrics", label: "Metrics", icon: Activity },
  { href: "/admin/telemetry/logs", label: "Logs", icon: Logs },
  { href: "/admin/telemetry/errors", label: "Errors", icon: CircleAlert },
  { href: "/admin/telemetry/traces", label: "Traces", icon: RadioTower },
  { href: "/admin/telemetry/services", label: "Service map", icon: Waypoints },
  { href: "/admin/telemetry/sources", label: "Sources", icon: DatabaseZap },
  { href: "/admin/status", label: "Status page", icon: Waypoints },
  { href: "/admin/audit", label: "Audit & security", icon: ShieldCheck },
];

export function AdminShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  return (
    <div>
      <nav
        aria-label="Admin navigation"
        className="mb-8 flex gap-1 overflow-x-auto border-b border-graphite pb-px"
      >
        {links.map((link) => {
          const active =
            pathname === link.href ||
            (link.href !== "/admin" && pathname.startsWith(link.href));
          const Icon = link.icon;
          return (
            <Link
              key={link.href}
              href={link.href}
              aria-current={active ? "page" : undefined}
              className={cn(
                "-mb-px inline-flex shrink-0 items-center gap-2 border-b-2 border-transparent px-3 py-2.5 text-sm text-fog transition-colors duration-150 hover:text-mist",
                active && "border-acid-lime text-paper",
              )}
            >
              <Icon className="size-3.5" aria-hidden="true" />
              {link.label}
            </Link>
          );
        })}
      </nav>
      {children}
    </div>
  );
}
