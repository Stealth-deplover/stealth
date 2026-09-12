"use client";
import Link from "next/link";
import { ArrowUpRight } from "lucide-react";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";
import { useCreateProject } from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { useProjects } from "@/api/queries";
import type { Project } from "@/api/types";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { pageControls } from "@/lib/pagination";
import {
  projectFields,
  projectPayload,
  type ProjectFormValues,
} from "@/features/organization/project-form";

export function OrganizationProjectList({
  organizationId,
}: {
  organizationId: string;
}) {
  const router = useRouter();
  const navigation = useCursorPagination();
  const [createOpen, setCreateOpen] = useState(false);
  const query = useProjects(organizationId, { cursor: navigation.cursor });
  const create = useCreateProject(organizationId);
  const canManage = query.data?.can_manage === true;
  const handleCreateProject = async (values: ProjectFormValues) => {
    const result = await create.mutateAsync(projectPayload(values));
    toast.success("Project created");
    if (result?.project) {
      router.push(
        `/organizations/${organizationId}/projects/${result.project.id}`,
      );
    }
  };
  const projects = query.data?.projects ?? [];
  if (query.isError)
    return (
      <ErrorState
        title="Could not load projects"
        error={query.error}
        retry={() => query.refetch()}
      />
    );
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-sm font-semibold text-white">Projects</h2>
          <p className="mt-1 text-xs text-slate-500">
            Each project maps to one isolated Stealth application boundary.
          </p>
        </div>
        {canManage ? (
          <CreateDialog<ProjectFormValues>
            open={createOpen}
            onOpenChange={setCreateOpen}
            triggerLabel="Create project"
            submitLabel="Create project"
            pendingLabel="Creating project…"
            title="Create a project"
            description="Project names are normalized to stable API slugs, for example Production API becomes production-api."
            fields={projectFields}
            pending={create.isPending}
            onSubmit={handleCreateProject}
          />
        ) : query.data ? (
          <Badge variant="neutral">Read-only</Badge>
        ) : null}
      </div>
      {query.isLoading ? (
        <DataTable
          columns={[{ accessorKey: "name", header: "Name" }]}
          data={[]}
          loading
        />
      ) : projects.length || navigation.canPrevious ? (
        <Card>
          <DataTable<Project>
            data={projects}
            serverPagination={pageControls(
              navigation,
              nextCursor(query.data),
              query.isFetching,
            )}
            empty="No projects returned on this page."
            columns={[
              {
                accessorKey: "name",
                header: "Project",
                cell: ({ row }) => (
                  <button
                    type="button"
                    className="font-medium text-white hover:text-cyan-200"
                    onClick={() =>
                      router.push(
                        `/organizations/${organizationId}/projects/${row.original.id}`,
                      )
                    }
                  >
                    {row.original.name}
                  </button>
                ),
              },
              {
                accessorKey: "id",
                header: "Project ID",
                cell: ({ row }) => (
                  <span className="font-mono text-xs text-slate-500">
                    {row.original.id.slice(0, 8)}…
                  </span>
                ),
              },
              {
                accessorKey: "created_at",
                header: "Created",
                cell: ({ row }) => formatDate(row.original.created_at),
              },
              {
                id: "action",
                header: "",
                cell: ({ row }) => (
                  <Button asChild variant="ghost" size="sm">
                    <Link
                      href={`/organizations/${organizationId}/projects/${row.original.id}`}
                    >
                      Open <ArrowUpRight className="size-3.5" />
                    </Link>
                  </Button>
                ),
              },
            ]}
          />
        </Card>
      ) : (
        <EmptyState
          title="No projects yet"
          description={
            canManage
              ? "Create a project to get a resource-aware developer console and a clean API boundary."
              : "No projects are available to manage in this organization."
          }
          actionLabel={canManage ? "Create project" : undefined}
          action={canManage ? () => setCreateOpen(true) : undefined}
        />
      )}
    </div>
  );
}

export function OrganizationProjectsView({
  organizationId,
}: {
  organizationId: string;
}) {
  return (
    <>
      <PageHeader
        eyebrow="Organization"
        title="Projects"
        description="Your project's control plane starts here."
      />
      <OrganizationProjectList organizationId={organizationId} />
    </>
  );
}
