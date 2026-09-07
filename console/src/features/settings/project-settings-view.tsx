"use client";
import { Check, CircleHelp } from "lucide-react";
import { useProject } from "@/api/queries";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { CopyButton } from "@/components/copy-button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { formatDate } from "@/lib/format";

export function ProjectSettingsView({ projectId }: { projectId: string }) {
  const project = useProject(projectId);
  if (project.isLoading) return <LoadingState rows={4} />;
  return (
    <>
      <PageHeader
        eyebrow="Project"
        title="Settings"
        description="Project metadata and backend-owned configuration boundaries."
      />
      {project.error ? (
        <ErrorState error={project.error} retry={() => project.refetch()} />
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle>Project identity</CardTitle>
            </CardHeader>
            <CardContent>
              <dl className="space-y-3 text-sm">
                <div>
                  <dt className="text-xs text-slate-600">Name</dt>
                  <dd className="mt-1 text-white">
                    {project.data?.project.name}
                  </dd>
                </div>
                <div>
                  <dt className="text-xs text-slate-600">Project ID</dt>
                  <dd className="mt-1 flex items-center gap-1.5 break-all font-mono text-xs text-slate-400">
                    <span className="break-all">
                      {project.data?.project.id}
                    </span>
                    {project.data?.project.id ? (
                      <CopyButton
                        value={project.data.project.id}
                        label="Copy project ID"
                        className="size-6 shrink-0 text-slate-600 hover:text-slate-200"
                      />
                    ) : null}
                  </dd>
                </div>
                <div>
                  <dt className="text-xs text-slate-600">Created</dt>
                  <dd className="mt-1 text-slate-300">
                    {formatDate(project.data?.project.created_at)}
                  </dd>
                </div>
              </dl>
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>Backend boundaries</CardTitle>
            </CardHeader>
            <CardContent>
              <ul className="space-y-3 text-sm leading-6 text-slate-500">
                <li className="flex gap-2">
                  <Check className="mt-1 size-4 shrink-0 text-emerald-300" />
                  Go API remains the source of truth.
                </li>
                <li className="flex gap-2">
                  <Check className="mt-1 size-4 shrink-0 text-emerald-300" />
                  Console requests use the session cookie.
                </li>
                <li className="flex gap-2">
                  <CircleHelp className="mt-1 size-4 shrink-0 text-amber-300" />
                  Source editor and dependency edges are not available in the
                  current contract.
                </li>
              </ul>
            </CardContent>
          </Card>
        </div>
      )}
    </>
  );
}
