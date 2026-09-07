"use client";

import { Bell, Command, LogOut, Menu, Search, UserRound } from "lucide-react";
import { useRouter } from "next/navigation";
import {
  OrganizationSwitcher,
  ProjectSwitcher,
  ContextBadge,
} from "@/components/navigation/context-switchers";
import { CommandPalette } from "@/components/navigation/command-palette";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useLogout } from "@/api/mutations";
import { toast } from "sonner";
import { Breadcrumbs } from "@/components/breadcrumbs";

export function Topbar({
  organizationId,
  projectId,
  onMenu,
}: {
  organizationId?: string;
  projectId?: string;
  onMenu?: () => void;
}) {
  const router = useRouter();
  const logout = useLogout();
  return (
    <header className="sticky top-0 z-30 flex min-h-[4.5rem] items-center justify-between gap-2 border-b border-stealth-border bg-stealth-bg/85 px-2.5 backdrop-blur-xl sm:gap-3 sm:px-6">
      <div className="flex min-w-0 items-center gap-1.5 sm:gap-2">
        <Button
          variant="ghost"
          size="icon"
          className="lg:hidden"
          onClick={onMenu}
          aria-label="Open navigation"
        >
          <Menu className="size-4" />
        </Button>
        <div className="flex min-w-0 items-center gap-1.5 sm:gap-3">
          <OrganizationSwitcher currentId={organizationId} />
          {organizationId ? (
            <ProjectSwitcher
              organizationId={organizationId}
              currentId={projectId}
            />
          ) : null}
        </div>
        <Breadcrumbs organizationId={organizationId} projectId={projectId} />
      </div>
      <div className="flex shrink-0 items-center gap-1.5 sm:gap-2">
        <ContextBadge projectId={projectId} />
        <Button
          variant="outline"
          size="sm"
          className="hidden gap-2 text-slate-500 md:flex"
          onClick={() =>
            window.dispatchEvent(
              new KeyboardEvent("keydown", { key: "k", ctrlKey: true }),
            )
          }
          aria-keyshortcuts="Control+K Meta+K"
        >
          <Search className="size-3.5" /> Search
          <span className="ml-2 rounded border border-stealth-border px-1.5 py-0.5 text-[10px] text-slate-600">
            ⌘ K
          </span>
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="hidden sm:inline-flex"
          aria-label="Notifications"
        >
          <Bell className="size-4 text-slate-500" />
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" aria-label="Account menu">
              <UserRound className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={() => router.push("/account")}>
              <UserRound className="size-4" /> Account
            </DropdownMenuItem>
            <DropdownMenuItem
              onClick={() =>
                window.dispatchEvent(
                  new KeyboardEvent("keydown", { key: "k", ctrlKey: true }),
                )
              }
            >
              <Command className="size-4" /> Command palette
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              onClick={() =>
                logout.mutate(undefined, {
                  onSuccess: () => {
                    toast.success("Signed out");
                    router.replace("/login");
                  },
                })
              }
            >
              <LogOut className="size-4" /> Sign out
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      <CommandPalette />
    </header>
  );
}
