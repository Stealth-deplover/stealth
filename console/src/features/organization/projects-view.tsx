"use client";
import Link from "next/link";
import { ArrowUpRight } from "lucide-react";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";
import { useCreateOrganization, useCreateProject } from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { useOrganizations, useProjects } from "@/api/queries";
import type { Project } from "@/api/types";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { CursorPaginationControls } from "@/components/cursor-pagination-controls";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { pageControls } from "@/lib/pagination";

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
  const handleCreateProject = async (values: Record<string, string>) => {
    await create.mutateAsync({ name: values.name });
    toast.success("Project created");
  };
  const projects = query.data?.projects ?? [];
  if (query.isError)
    return <ErrorState error={query.error} retry={() => query.refetch()} />;
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-sm font-semibold text-white">Projects</h2>
          <p className="mt-1 text-xs text-slate-500">
            Each project maps to one isolated Stealth application boundary.
          </p>
        </div>
        <CreateDialog
          open={createOpen}
          onOpenChange={setCreateOpen}
          label="Project"
          title="Create a project"
          description="Project names are normalized to stable API slugs, for example Production API becomes production-api."
          fields={[
            {
              name: "name",
              label: "Project name",
              placeholder: "Production API",
              help: "Use a readable name; the console sends the lowercase hyphenated slug required by the API.",
            },
          ]}
          pending={create.isPending}
          onSubmit={handleCreateProject}
        />
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
          description="Create a project to get a resource-aware developer console and a clean API boundary."
          actionLabel="Create project"
          action={() => setCreateOpen(true)}
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

export function OrganizationsIndexView() {
  const router = useRouter();
  const navigation = useCursorPagination();
  const query = useOrganizations({ cursor: navigation.cursor });
  const create = useCreateOrganization();
  const handleCreateOrganization = async (values: Record<string, string>) => {
    await create.mutateAsync({
      name: values.name,
      slug: values.slug,
    });
    toast.success("Organization created");
  };
  const organizations = query.data?.organizations ?? [];
  return (
    <>
      <PageHeader
        eyebrow="Account"
        title="Organizations"
        description="Choose a workspace, then open a project to operate its services."
        actions={
          <CreateDialog
            label="Organization"
            title="Create an organization"
            description="Organizations group people, projects, and plan limits."
            fields={[
              { name: "name", label: "Display name", placeholder: "Acme Inc" },
              {
                name: "slug",
                label: "Slug",
                placeholder: "acme-inc",
                help: "Lowercase letters, numbers, and hyphens.",
              },
            ]}
            pending={create.isPending}
            onSubmit={handleCreateOrganization}
          />
        }
      />
      {query.isError ? (
        <ErrorState error={query.error} retry={() => query.refetch()} />
      ) : organizations.length || navigation.canPrevious ? (
        <>
          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
            {organizations.map((organization) => (
              <Card
                key={organization.id}
                className="group transition hover:border-cyan-300/30"
              >
                <CardContent className="p-5">
                  <div className="flex items-start justify-between">
                    <span className="flex size-10 items-center justify-center rounded-xl border border-cyan-300/20 bg-cyan-300/10 text-sm font-semibold text-cyan-200">
                      {organization.name.slice(0, 1).toUpperCase()}
                    </span>
                    <Badge variant="neutral">Workspace</Badge>
                  </div>
                  <h2 className="mt-5 text-lg font-semibold text-white">
                    {organization.name}
                  </h2>
                  <p className="mt-1 font-mono text-xs text-slate-600">
                    {organization.slug}
                  </p>
                  <p className="mt-5 text-xs text-slate-500">
                    Created {formatDate(organization.created_at)}
                  </p>
                  <Button
                    className="mt-5 w-full"
                    variant="outline"
                    onClick={() =>
                      router.push(`/organizations/${organization.id}/projects`)
                    }
                  >
                    Open workspace <ArrowUpRight className="size-3.5" />
                  </Button>
                </CardContent>
              </Card>
            ))}
          </div>
          <CursorPaginationControls
            {...pageControls(
              navigation,
              nextCursor(query.data),
              query.isFetching,
            )}
            label={`${organizations.length} organizations on this page`}
          />
        </>
      ) : (
        <EmptyState
          title="No organizations yet"
          description="Create a workspace to start mapping projects to the Stealth API."
        />
      )}
    </>
  );
}
