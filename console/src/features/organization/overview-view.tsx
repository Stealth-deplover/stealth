"use client";
import Link from "next/link";
import { Activity, FolderKanban, Users } from "lucide-react";
import { useOrganization, useOrganizationPlan } from "@/api/queries";
import { PageHeader } from "@/components/page-header";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { formatCount } from "@/lib/format";
import { OrganizationProjectList } from "./projects-view";

export function OrganizationOverviewView({
  organizationId,
}: {
  organizationId: string;
}) {
  const organization = useOrganization(organizationId);
  const plan = useOrganizationPlan(organizationId);
  const current = organization.data;
  const currentPlan = plan.data?.plan;
  const overview = (() => {
    if (organization.isPending || plan.isPending) {
      return <LoadingState rows={3} />;
    }
    if (organization.isError) {
      return (
        <ErrorState
          title="Could not load organization"
          error={organization.error}
          retry={() => organization.refetch()}
        />
      );
    }
    if (plan.isError) {
      return (
        <ErrorState
          title="Could not load organization plan"
          error={plan.error}
          retry={() => plan.refetch()}
        />
      );
    }
    if (!current || !currentPlan) {
      return (
        <EmptyState
          title="Organization overview unavailable"
          description="The API did not return the organization or plan data needed for this overview."
        />
      );
    }
    return (
      <>
        <PageHeader
          eyebrow="Organization"
          title={current.name}
          description="A clear view of your teams, projects, and operating limits."
          actions={
            <Button asChild variant="outline">
              <Link href={`/organizations/${organizationId}/projects`}>
                <FolderKanban className="size-4" /> Open projects
              </Link>
            </Button>
          }
        />
        <div className="grid gap-4 md:grid-cols-3">
          <Card>
            <CardContent className="p-5">
              <div className="flex items-center justify-between">
                <p className="text-xs uppercase tracking-[0.14em] text-slate-600">
                  Projects
                </p>
                <FolderKanban className="size-4 text-cyan-300" />
              </div>
              <p className="mt-3 text-3xl font-semibold text-white">
                {formatCount(currentPlan.usage.projects)}
              </p>
            </CardContent>
          </Card>
          <Card>
            <CardContent className="p-5">
              <div className="flex items-center justify-between">
                <p className="text-xs uppercase tracking-[0.14em] text-slate-600">
                  Plan
                </p>
                <Activity className="size-4 text-violet-300" />
              </div>
              <p className="mt-3 text-3xl font-semibold capitalize text-white">
                {currentPlan.plan_key}
              </p>
            </CardContent>
          </Card>
          <Card>
            <CardContent className="p-5">
              <div className="flex items-center justify-between">
                <p className="text-xs uppercase tracking-[0.14em] text-slate-600">
                  Members
                </p>
                <Users className="size-4 text-amber-300" />
              </div>
              <p className="mt-3 text-3xl font-semibold text-white">
                {formatCount(currentPlan.usage.members)}
              </p>
            </CardContent>
          </Card>
        </div>
      </>
    );
  })();

  return (
    <>
      {overview}
      <div className="mt-8">
        <OrganizationProjectList organizationId={organizationId} />
      </div>
    </>
  );
}
