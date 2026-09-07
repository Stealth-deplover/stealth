"use client";
import { ArrowUpRight } from "lucide-react";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";
import { useCreateOrganization } from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { useOrganizations } from "@/api/queries";
import { CreateDialog } from "@/components/create-dialog";
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

export function OrganizationsIndexView() {
  const router = useRouter();
  const navigation = useCursorPagination();
  const [createOpen, setCreateOpen] = useState(false);
  const query = useOrganizations({ cursor: navigation.cursor });
  const create = useCreateOrganization();
  const handleCreateOrganization = async (values: Record<string, string>) => {
    const result = await create.mutateAsync({
      name: values.name,
      slug: values.slug,
    });
    toast.success("Organization created");
    if (result?.organization) {
      router.replace(`/organizations/${result.organization.id}/projects`);
    }
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
            open={createOpen}
            onOpenChange={setCreateOpen}
            triggerLabel="Create organization"
            submitLabel="Create organization"
            pendingLabel="Creating organization…"
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
      ) : query.isLoading ? (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 3 }, (_, index) => (
            <Card key={index}>
              <CardContent className="space-y-4 p-5">
                <div className="h-10 w-10 animate-pulse rounded-xl bg-white/[0.06]" />
                <div className="h-5 w-36 animate-pulse rounded bg-white/[0.06]" />
                <div className="h-3 w-24 animate-pulse rounded bg-white/[0.04]" />
              </CardContent>
            </Card>
          ))}
        </div>
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
          title="Welcome to Stealth"
          description="Create an organization to start managing projects and resources."
          actionLabel="Create organization"
          action={() => setCreateOpen(true)}
        />
      )}
    </>
  );
}
