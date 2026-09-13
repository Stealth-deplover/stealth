"use client";
import { useOrganizationPlan } from "@/api/queries";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { StatusBadge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";

export function OrganizationPlanView({
  organizationId,
}: {
  organizationId: string;
}) {
  const query = useOrganizationPlan(organizationId);
  const plan = query.data?.plan;
  if (query.isError)
    return (
      <ErrorState
        title="Could not load organization plan"
        error={query.error}
        retry={() => query.refetch()}
      />
    );
  if (query.isLoading) return <LoadingState rows={4} />;
  return (
    <>
      <PageHeader
        eyebrow="Organization"
        title="Plan & limits"
        description="Read-only plan data from the backend. Billing workflows are not exposed by the current API."
      />
      {plan ? (
        <div className="grid gap-4 lg:grid-cols-[1fr_1fr]">
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center justify-between">
                <span className="capitalize">{plan.plan_key} plan</span>
                <StatusBadge status={plan.status} />
              </CardTitle>
            </CardHeader>
            <CardContent>
              <div className="grid grid-cols-2 gap-3">
                {Object.entries(plan.limits).map(([key, limit]) => (
                  <div
                    key={key}
                    className="rounded-lg border border-stealth-border bg-black/10 p-3"
                  >
                    <p className="text-xs capitalize text-slate-500">
                      {key.replace(/_/g, " ")}
                    </p>
                    <p className="mt-1 text-lg font-semibold text-white">
                      {limit === -1 ? "Unlimited" : limit}
                    </p>
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>Current usage</CardTitle>
            </CardHeader>
            <CardContent>
              <div className="grid grid-cols-2 gap-3">
                {Object.entries(plan.usage).map(([key, value]) => (
                  <div
                    key={key}
                    className="rounded-lg border border-stealth-border bg-black/10 p-3"
                  >
                    <p className="text-xs capitalize text-slate-500">
                      {key.replace(/_/g, " ")}
                    </p>
                    <p className="mt-1 text-lg font-semibold text-white">
                      {value}
                    </p>
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>
        </div>
      ) : (
        <EmptyState
          title="Plan unavailable"
          description="The API did not return plan data for this organization."
        />
      )}
    </>
  );
}
