"use client";

import { Command, LogOut, Menu, Search, UserRound } from "lucide-react";
import type { RefObject } from "react";
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
import { useConsoleRouteContext } from "@/components/navigation/console-route-context";

export function Topbar({
  onMenu,
  menuButtonRef,
}: {
  onMenu?: () => void;
  menuButtonRef?: RefObject<HTMLButtonElement | null>;
}) {
  const router = useRouter();
  const logout = useLogout();
  const { organizationId, projectId } = useConsoleRouteContext();
  return (
    <header className="sticky top-0 z-30 flex min-h-16 items-center justify-between gap-2 border-b border-graphite bg-void/95 px-2.5 sm:gap-3 sm:px-6">
      <div className="flex min-w-0 items-center gap-1.5 sm:gap-2">
        <Button
          ref={menuButtonRef}
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
        <Breadcrumbs />
      </div>
      <div className="flex shrink-0 items-center gap-1.5 sm:gap-2">
        <ContextBadge projectId={projectId} />
        <Button
          variant="outline"
          size="sm"
          className="hidden gap-2 text-fog md:flex"
          onClick={() =>
            window.dispatchEvent(
              new KeyboardEvent("keydown", { key: "k", ctrlKey: true }),
            )
          }
          aria-keyshortcuts="Control+K Meta+K"
        >
          <Search className="size-3.5" /> Search
          <span className="ml-2 rounded-sm border border-graphite px-1.5 py-0.5 font-mono text-[10px] text-fog">
            ⌘ K
          </span>
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
