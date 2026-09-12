"use client";

import { Command, FolderKanban, Search, Waypoints } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { useOrganizations, useProjects } from "@/api/queries";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";

type PaletteCommand = {
  label: string;
  hint: string;
  href: string;
  kind: "navigation" | "organization" | "project";
};

export function CommandPalette() {
  const router = useRouter();
  const pathname = usePathname();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const buttons = useRef<Array<HTMLButtonElement | null>>([]);
  const segments = pathname.split("/").filter(Boolean);
  const organizationId =
    segments[0] === "organizations" ? segments[1] : undefined;
  const projectId =
    organizationId && segments[2] === "projects" ? segments[3] : undefined;
  const organizations = useOrganizations(undefined, { enabled: open });
  const projects = useProjects(organizationId, undefined, { enabled: open });
  const base = projectId
    ? `/organizations/${organizationId}/projects/${projectId}`
    : organizationId
      ? `/organizations/${organizationId}`
      : "/organizations";

  const commands = useMemo<PaletteCommand[]>(() => {
    const navigation: PaletteCommand[] = [
      {
        label: "Project overview",
        hint: "View current project",
        href: base,
        kind: "navigation",
      },
      {
        label: "Services canvas",
        hint: "Map supported resources",
        href: `${base}/services`,
        kind: "navigation",
      },
      {
        label: "Deployments",
        hint: "Inspect resource deployments",
        href: `${base}/deployments`,
        kind: "navigation",
      },
      {
        label: "Functions",
        hint: "Run backend code",
        href: `${base}/functions`,
        kind: "navigation",
      },
      {
        label: "Databases",
        hint: "Browse schema and rows",
        href: `${base}/databases`,
        kind: "navigation",
      },
      {
        label: "Storage",
        hint: "Explore buckets and files",
        href: `${base}/storage`,
        kind: "navigation",
      },
      {
        label: "Logs",
        hint: "Open contextual log viewer",
        href: `${base}/observability/logs`,
        kind: "navigation",
      },
      {
        label: "Traces",
        hint: "Inspect durable root requests",
        href: `${base}/observability/traces`,
        kind: "navigation",
      },
      {
        label: "Settings",
        hint: "Project configuration",
        href: `${base}/settings/project`,
        kind: "navigation",
      },
    ].filter(
      (command) => Boolean(projectId) && command.href.startsWith(base),
    ) as PaletteCommand[];
    const workspaceCommands: PaletteCommand[] = (
      organizations.data?.organizations ?? []
    ).map((organization) => ({
      label: `Switch organization · ${organization.name}`,
      hint: organization.slug,
      href: `/organizations/${organization.id}/projects`,
      kind: "organization" as const,
    }));
    const projectCommands: PaletteCommand[] = (
      projects.data?.projects ?? []
    ).map((project) => ({
      label: `Switch project · ${project.name}`,
      hint: "Project",
      href: `/organizations/${project.organization_id}/projects/${project.id}`,
      kind: "project" as const,
    }));
    const searchTerm = query.trim().toLowerCase();
    return [...navigation, ...workspaceCommands, ...projectCommands].filter(
      (command) =>
        !searchTerm ||
        `${command.label} ${command.hint}`.toLowerCase().includes(searchTerm),
    );
  }, [
    base,
    organizations.data?.organizations,
    projectId,
    projects.data?.projects,
    query,
  ]);

  const selectedIndex = commands.length
    ? Math.min(activeIndex, commands.length - 1)
    : 0;
  useEffect(() => {
    buttons.current[selectedIndex]?.scrollIntoView({ block: "nearest" });
  }, [selectedIndex]);
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setOpen(true);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  const close = () => {
    setOpen(false);
    setQuery("");
    setActiveIndex(0);
  };
  const handleKeyDown = (event: React.KeyboardEvent) => {
    if (!commands.length) return;
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setActiveIndex((index) => (index + 1) % commands.length);
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      setActiveIndex(
        (index) => (index - 1 + commands.length) % commands.length,
      );
    }
    if (event.key === "Enter") {
      event.preventDefault();
      close();
      router.push(commands[selectedIndex].href);
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(value) => {
        if (value) setOpen(true);
        else close();
      }}
    >
      <DialogContent className="max-w-xl p-3" onKeyDown={handleKeyDown}>
        <DialogHeader className="sr-only">
          <DialogTitle>Command palette</DialogTitle>
          <DialogDescription>
            Navigate through organizations, projects, and console resources.
          </DialogDescription>
        </DialogHeader>
        <div className="relative">
          <Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-slate-600" />
          <Input
            autoFocus
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search the console…"
            className="h-11 border-transparent bg-black/20 pl-9 text-sm focus:border-cyan-300/40"
            role="combobox"
            aria-controls="console-command-list"
            aria-expanded="true"
            aria-activedescendant={
              commands[selectedIndex]
                ? `console-command-${selectedIndex}`
                : undefined
            }
          />
        </div>
        <div
          id="console-command-list"
          className="mt-2 max-h-80 overflow-y-auto"
          role="listbox"
          aria-label="Console commands"
        >
          {commands.length ? (
            commands.map((command, index) => (
              <button
                ref={(element) => {
                  buttons.current[index] = element;
                }}
                type="button"
                id={`console-command-${index}`}
                role="option"
                aria-selected={index === selectedIndex}
                key={`${command.kind}-${command.href}`}
                onMouseEnter={() => setActiveIndex(index)}
                onClick={() => {
                  close();
                  router.push(command.href);
                }}
                className={`flex w-full items-center gap-3 rounded-xl px-3 py-3 text-left ${index === selectedIndex ? "bg-cyan-300/10" : "hover:bg-white/[0.06]"}`}
              >
                <span className="flex size-8 items-center justify-center rounded-lg border border-white/10 bg-white/[0.03] text-slate-400">
                  {command.kind === "project" ? (
                    <FolderKanban className="size-4" />
                  ) : command.kind === "organization" ? (
                    <Waypoints className="size-4" />
                  ) : (
                    <Command className="size-4" />
                  )}
                </span>
                <span className="flex-1">
                  <span className="block text-sm font-medium text-slate-100">
                    {command.label}
                  </span>
                  <span className="block text-xs text-slate-500">
                    {command.hint}
                  </span>
                </span>
                <span className="text-[10px] text-slate-600">↵</span>
              </button>
            ))
          ) : (
            <p className="px-3 py-8 text-center text-sm text-slate-500">
              No matching command.
            </p>
          )}
        </div>
        <div className="mt-2 flex items-center justify-between border-t border-stealth-border px-3 pt-3 text-[10px] text-slate-600">
          <span>↑↓ navigate · Enter open</span>
          <span className="rounded border border-stealth-border px-1.5 py-0.5">
            Esc
          </span>
        </div>
      </DialogContent>
    </Dialog>
  );
}
