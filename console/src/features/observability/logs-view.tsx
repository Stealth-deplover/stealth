"use client";
import Link from "next/link";
import { AppWindow, TerminalSquare } from "lucide-react";
import { nextCursor } from "@/api/pagination";
import { useApps, useFunctions, useSites } from "@/api/queries";
import { CursorPaginationControls } from "@/components/cursor-pagination-controls";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent } from "@/components/ui/card";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";

export function LogsView({
  organizationId,
  projectId,
}: {
  organizationId: string;
  projectId: string;
}) {
  const functionsNavigation = useCursorPagination("log_functions_cursor");
  const sitesNavigation = useCursorPagination("log_sites_cursor");
  const appsNavigation = useCursorPagination("log_apps_cursor");
  const functions = useFunctions(projectId, {
    cursor: functionsNavigation.cursor,
  });
  const sites = useSites(projectId, { cursor: sitesNavigation.cursor });
  const apps = useApps(projectId, { cursor: appsNavigation.cursor });
  if (functions.error || sites.error || apps.error)
    return (
      <ErrorState
        title="Could not load log sources"
        error={functions.error ?? sites.error ?? apps.error}
        retry={() => {
          void functions.refetch();
          void sites.refetch();
          void apps.refetch();
        }}
      />
    );
  if (functions.isLoading || sites.isLoading || apps.isLoading) return <LoadingState rows={4} />;
  return (
    <>
      <PageHeader
        eyebrow="Observability"
        title="Logs"
        description="Functions provide build and execution logs, Sites provide build logs, and Apps provide build and runtime logs."
      />
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {functions.data?.functions.map((item) => (
          <Card key={item.id}>
            <CardContent className="p-5">
              <FunctionSquareIcon />
              <h2 className="mt-4 text-sm font-semibold text-white">
                {item.name}
              </h2>
              <p className="mt-1 text-xs leading-5 text-slate-500">
                Build and execution logs are available from the Function detail
                view.
              </p>
              <Link
                href={`/organizations/${organizationId}/projects/${projectId}/functions/${item.id}`}
                className="mt-4 inline-flex text-xs text-cyan-300"
              >
                Open function →
              </Link>
            </CardContent>
          </Card>
        ))}
        {sites.data?.sites.map((item) => (
          <Card key={item.id}>
            <CardContent className="p-5">
              <TerminalSquare className="size-5 text-violet-300" />
              <h2 className="mt-4 text-sm font-semibold text-white">
                {item.name}
              </h2>
              <p className="mt-1 text-xs leading-5 text-slate-500">
                Site build logs are scoped to each site deployment.
              </p>
              <Link
                href={`/organizations/${organizationId}/projects/${projectId}/sites/${item.id}`}
                className="mt-4 inline-flex text-xs text-mist hover:text-paper"
              >
                Open site →
              </Link>
            </CardContent>
          </Card>
        ))}
        {apps.data?.apps.map((item) => (
          <Card key={item.id}>
            <CardContent className="p-5">
              <AppWindow className="size-5 text-signal-teal" aria-hidden="true" />
              <h2 className="mt-4 text-sm font-semibold text-white">{item.name}</h2>
              <p className="mt-1 text-xs leading-5 text-slate-500">
                App build output and retained stdout/stderr from verified runtime containers.
              </p>
              <Link
                href={`/organizations/${organizationId}/projects/${projectId}/apps/${item.id}`}
                className="mt-4 inline-flex min-h-11 items-center text-xs text-mist hover:text-paper"
              >
                Open App logs →
              </Link>
            </CardContent>
          </Card>
        ))}
      </div>
      <div className="mt-4 grid gap-4 md:grid-cols-3">
        <Card>
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
            label="Function log sources"
          />
        </Card>
        <Card>
          <CursorPaginationControls
            canFirst={sitesNavigation.canFirst}
            canPrevious={sitesNavigation.canPrevious}
            canNext={Boolean(nextCursor(sites.data))}
            onFirst={sitesNavigation.goFirst}
            onPrevious={sitesNavigation.goPrevious}
            onNext={() => sitesNavigation.goNext(nextCursor(sites.data))}
            isFetching={sites.isFetching}
            label="Site log sources"
          />
        </Card>
        <Card>
          <CursorPaginationControls
            canFirst={appsNavigation.canFirst}
            canPrevious={appsNavigation.canPrevious}
            canNext={Boolean(nextCursor(apps.data))}
            onFirst={appsNavigation.goFirst}
            onPrevious={appsNavigation.goPrevious}
            onNext={() => appsNavigation.goNext(nextCursor(apps.data))}
            isFetching={apps.isFetching}
            label="App log sources"
          />
        </Card>
      </div>
      {!functions.data?.functions.length && !sites.data?.sites.length && !apps.data?.apps.length ? (
        <div className="mt-4">
          <EmptyState
            title="No log sources yet"
            description="Create a Function, deploy a Site, or run an App to populate this project's logs."
          />
        </div>
      ) : null}
    </>
  );
}

function FunctionSquareIcon() {
  return <TerminalSquare className="size-5 text-cyan-300" />;
}
