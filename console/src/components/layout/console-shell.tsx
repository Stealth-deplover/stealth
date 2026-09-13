"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useCurrentAccount } from "@/api/queries";
import { ApiError } from "@/api/client";
import { ErrorState } from "@/components/feedback/error-state";
import { Skeleton } from "@/components/ui/skeleton";
import { Sidebar } from "@/components/layout/sidebar";
import { Topbar } from "@/components/layout/topbar";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  ConsoleRouteContextProvider,
  useConsoleRouteContext,
} from "@/components/navigation/console-route-context";
import { ProjectRealtimeListener } from "@/realtime/project-realtime-listener";

function ConsoleShellContent({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  const router = useRouter();
  const { pathname, projectId } = useConsoleRouteContext();
  const account = useCurrentAccount();
  const [mobileOpen, setMobileOpen] = useState(false);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const mobileMenuTriggerRef = useRef<HTMLButtonElement>(null);
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
      <ProjectRealtimeListener projectId={projectId} />
      <div className="flex min-h-screen">
        <Sidebar
          collapsed={sidebarCollapsed}
          onToggle={() => setSidebarCollapsed((value) => !value)}
        />
        <Dialog open={mobileOpen} onOpenChange={setMobileOpen}>
          <DialogContent
            className="left-0 top-0 h-full max-h-full w-72 max-w-none translate-x-0 translate-y-0 rounded-none border-y-0 border-l-0 p-0"
            onCloseAutoFocus={(event) => {
              event.preventDefault();
              mobileMenuTriggerRef.current?.focus();
            }}
          >
            <DialogHeader className="sr-only">
              <DialogTitle>Navigation menu</DialogTitle>
              <DialogDescription>
                Move between Stealth workspaces and resources.
              </DialogDescription>
            </DialogHeader>
            <Sidebar mobile />
          </DialogContent>
        </Dialog>
        <div className="min-w-0 flex-1">
          <Topbar
            onMenu={() => setMobileOpen(true)}
            menuButtonRef={mobileMenuTriggerRef}
          />
          <main className="mx-auto w-full max-w-[1600px] px-4 py-7 sm:px-6 lg:px-10">
            {children}
          </main>
        </div>
      </div>
    </div>
  );
}

export function ConsoleShell({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <ConsoleRouteContextProvider>
      <ConsoleShellContent>{children}</ConsoleShellContent>
    </ConsoleRouteContextProvider>
  );
}
