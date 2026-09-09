"use client";
import Link from "next/link";
import { nextCursor } from "@/api/pagination";
import { useFunctions, useSites } from "@/api/queries";
import { CursorPaginationControls } from "@/components/cursor-pagination-controls";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { StatusBadge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";

export function DeploymentsView({
  organizationId,
  projectId,
}: {
  organizationId: string;
  projectId: string;
}) {
  const functionsNavigation = useCursorPagination(
    "deployment_functions_cursor",
  );
  const sitesNavigation = useCursorPagination("deployment_sites_cursor");
  const functions = useFunctions(projectId, {
    cursor: functionsNavigation.cursor,
  });
  const sites = useSites(projectId, { cursor: sitesNavigation.cursor });
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  if (functions.error || sites.error)
    return (
      <ErrorState
        title="Could not load deployments"
        error={functions.error ?? sites.error}
        retry={() => {
          void functions.refetch();
          void sites.refetch();
        }}
      />
    );
  return (
    <>
      <PageHeader
        eyebrow="Compute"
        title="Deployments"
        description="Inspect immutable deployment history for Functions and Sites."
      />
      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Functions</CardTitle>
            <p className="mt-1 text-xs text-slate-500">
              Functions on this API page.
            </p>
          </CardHeader>
          <CardContent className="space-y-3">
            {functions.data?.functions.length ? (
              functions.data.functions.map((item) => (
                <Link
                  key={item.id}
                  href={`${base}/functions/${item.id}`}
                  className="flex items-center justify-between rounded-lg border border-stealth-border p-3 hover:border-cyan-300/30"
                >
                  <span>
                    <span className="block text-sm font-medium text-white">
                      {item.name}
                    </span>
                    <span className="text-xs text-slate-600">
                      {item.active_deployment_id
                        ? "Active deployment available"
                        : "No active deployment"}
                    </span>
                  </span>
                  <StatusBadge
                    status={item.active_deployment_id ? "active" : "inactive"}
                  />
                </Link>
              ))
            ) : (
              <p className="text-sm text-slate-500">
                No functions on this page.
              </p>
            )}
          </CardContent>
          <CursorPaginationControls
            canFirst={functionsNavigation.canFirst}
            canPrevious={functionsNavigation.canPrevious}
            canNext={Boolean(nextCursor(functions.data))}
            onFirst={functionsNavigation.goFirst}
            onPrevious={functionsNavigation.goPrevious}
            onNext={() =>
              functionsNavigation.goNext(nextCursor(functions.data))
            }
            isFetching={functions.isFetching}
            label="Function page"
          />
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Sites</CardTitle>
            <p className="mt-1 text-xs text-slate-500">
              Sites on this API page.
            </p>
          </CardHeader>
          <CardContent className="space-y-3">
            {sites.data?.sites.length ? (
              sites.data.sites.map((item) => (
                <Link
                  key={item.id}
                  href={`${base}/sites/${item.id}`}
                  className="flex items-center justify-between rounded-lg border border-stealth-border p-3 hover:border-violet-300/30"
                >
                  <span>
                    <span className="block text-sm font-medium text-white">
                      {item.name}
                    </span>
                    <span className="text-xs text-slate-600">
                      {item.active_deployment_id
                        ? "Active deployment available"
                        : "No active deployment"}
                    </span>
                  </span>
                  <StatusBadge
                    status={item.active_deployment_id ? "active" : "inactive"}
                  />
                </Link>
              ))
            ) : (
              <p className="text-sm text-slate-500">No sites on this page.</p>
            )}
          </CardContent>
          <CursorPaginationControls
            canFirst={sitesNavigation.canFirst}
            canPrevious={sitesNavigation.canPrevious}
            canNext={Boolean(nextCursor(sites.data))}
            onFirst={sitesNavigation.goFirst}
            onPrevious={sitesNavigation.goPrevious}
            onNext={() => sitesNavigation.goNext(nextCursor(sites.data))}
            isFetching={sites.isFetching}
            label="Site page"
          />
        </Card>
      </div>
    </>
  );
}
