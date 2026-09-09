"use client";

import { useEffect, useMemo, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { useCurrentAccount } from "@/api/queries";
import { ApiError } from "@/api/client";
import { ErrorState } from "@/components/feedback/error-state";
import { Skeleton } from "@/components/ui/skeleton";
import { Sidebar } from "@/components/layout/sidebar";
import { Topbar } from "@/components/layout/topbar";
import { ProjectRealtimeListener } from "@/realtime/project-realtime-listener";

function contextFromPath(pathname: string) {
  const parts = pathname.split("/").filter(Boolean);
  const organizationIndex = parts.indexOf("organizations");
  const organizationId =
    organizationIndex >= 0 ? parts[organizationIndex + 1] : undefined;
  const projectsIndex = parts.indexOf("projects");
  const projectId = projectsIndex >= 0 ? parts[projectsIndex + 1] : undefined;
  return { organizationId, projectId };
}

export function ConsoleShell({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  const router = useRouter();
  const pathname = usePathname();
  const account = useCurrentAccount();
  const [mobileOpen, setMobileOpen] = useState(false);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const context = useMemo(() => contextFromPath(pathname), [pathname]);
  const unauthorized =
    account.error instanceof ApiError && account.error.status === 401;

  useEffect(() => {
    if (unauthorized) router.replace("/login");
  }, [router, unauthorized]);
  useEffect(() => {
    const handle = window.setTimeout(() => setMobileOpen(false), 0);
    return () => window.clearTimeout(handle);
  }, [pathname]);

  if (account.isPending)
    return (
      <div className="flex min-h-screen items-center justify-center bg-stealth-bg">
        <div className="w-72 space-y-3">
          <Skeleton className="mx-auto size-12 rounded-2xl" />
          <Skeleton className="h-4 w-40 mx-auto" />
          <Skeleton className="h-3 w-56 mx-auto" />
        </div>
      </div>
    );
  if (unauthorized)
    return (
      <div className="flex min-h-screen items-center justify-center bg-stealth-bg">
        <div className="w-72 space-y-3">
          <Skeleton className="mx-auto size-12 rounded-2xl" />
          <p className="text-center text-xs text-slate-500">
            Session expired. Returning to sign in…
          </p>
        </div>
      </div>
    );
  if (account.error)
    return (
      <main className="mx-auto flex min-h-screen max-w-2xl items-center px-6">
        <ErrorState
          title="Could not load account"
          error={account.error}
          retry={() => account.refetch()}
        />
      </main>
    );

  return (
    <div className="min-h-screen bg-stealth-bg">
      <ProjectRealtimeListener projectId={context.projectId} />
      <div className="flex min-h-screen">
        <Sidebar
          {...context}
          collapsed={sidebarCollapsed}
          onToggle={() => setSidebarCollapsed((value) => !value)}
        />
        {mobileOpen ? (
          <div className="fixed inset-0 z-40 lg:hidden">
            <button
              type="button"
              className="absolute inset-0 cursor-default bg-black/70"
              aria-label="Close navigation"
              onClick={() => setMobileOpen(false)}
            />
            <div
              className="relative z-10 h-full w-72"
              role="dialog"
              aria-label="Navigation menu"
            >
              <Sidebar {...context} mobile />
            </div>
          </div>
        ) : null}
        <div className="min-w-0 flex-1">
          <Topbar {...context} onMenu={() => setMobileOpen(true)} />
          <main className="mx-auto w-full max-w-[1600px] px-4 py-7 sm:px-6 lg:px-10">
            {children}
          </main>
        </div>
      </div>
    </div>
  );
}
