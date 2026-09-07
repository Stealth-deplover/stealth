"use client";
import Link from "next/link";
import { Activity, FolderKanban, Users } from "lucide-react";
import { useOrganization, useOrganizationPlan } from "@/api/queries";
import { PageHeader } from "@/components/page-header";
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
  return (
    <>
      <PageHeader
        eyebrow="Organization"
        title={current?.name ?? "Organization"}
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
              {formatCount(plan.data?.plan.usage.projects)}
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
              {plan.data?.plan.plan_key ?? "—"}
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
              {formatCount(plan.data?.plan.usage.members)}
            </p>
          </CardContent>
        </Card>
      </div>
      <div className="mt-8">
        <OrganizationProjectList organizationId={organizationId} />
      </div>
    </>
  );
}
