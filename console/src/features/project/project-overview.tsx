"use client";

import Link from "next/link";
import {
  ArrowUpRight,
  AlertTriangle,
  Box,
  Database,
  FunctionSquare,
  Globe2,
  HardDrive,
  History,
  RefreshCw,
  Users,
} from "lucide-react";
import {
  useProject,
  useProjectAudit,
  useProjectUsage,
  useStorageBuckets,
} from "@/api/queries";
import type { ProjectUsage } from "@/api/types";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ResourceId } from "@/components/resource-id";
import { formatBytes, formatCount, formatDate } from "@/lib/format";

function Metric({
  label,
  value,
  icon: Icon,
  href,
}: {
  label: string;
  value: string | number;
  icon: typeof Box;
  href: string;
}) {
  return (
    <Link
      href={href}
      className="group rounded-xl border border-stealth-border bg-stealth-panel p-4 transition hover:border-cyan-300/30 hover:bg-white/[0.03]"
    >
      <div className="flex items-center justify-between">
        <p className="text-xs text-slate-500">{label}</p>
        <Icon className="size-4 text-slate-600 transition group-hover:text-cyan-300" />
      </div>
      <p className="mt-3 text-2xl font-semibold text-white">{value}</p>
      <span className="mt-2 inline-flex items-center gap-1 text-[11px] text-slate-600 group-hover:text-cyan-300">
        Open resource <ArrowUpRight className="size-3" />
      </span>
    </Link>
  );
}

type ProjectResourceUsage = Pick<
  ProjectUsage,
  "function_count" | "site_count" | "database_count"
>;

export function isEmptyProject(
  usage: ProjectResourceUsage | undefined,
  storageBucketCount: number | undefined,
) {
  if (!usage || storageBucketCount === undefined) return false;
  return (
    usage.function_count === 0 &&
    usage.site_count === 0 &&
    usage.database_count === 0 &&
    storageBucketCount === 0
  );
}

export function shouldShowQuickStart(
  canManage: boolean | undefined,
  usage: ProjectResourceUsage | undefined,
  storageBucketCount: number | undefined,
) {
  return canManage === true && isEmptyProject(usage, storageBucketCount);
}

function OverviewSectionError({
  title,
  retry,
}: {
  title: string;
  retry?: () => void;
}) {
  return (
    <div
      className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-rose-300/20 bg-rose-400/[0.04] p-4"
      role="alert"
    >
      <p className="flex items-center gap-2 text-sm text-rose-200">
        <AlertTriangle className="size-4 shrink-0" /> {title}
      </p>
      {retry ? (
        <Button variant="outline" size="sm" onClick={retry}>
          <RefreshCw className="size-3.5" /> Try again
        </Button>
      ) : null}
    </div>
  );
}

function QuickStart({ base }: { base: string }) {
  const items = [
    {
      label: "Create function",
      description: "Run backend code on demand.",
      href: `${base}/functions`,
      icon: FunctionSquare,
    },
    {
      label: "Create site",
      description: "Publish a static application.",
      href: `${base}/sites`,
      icon: Globe2,
    },
    {
      label: "Create database",
      description: "Start a typed data layer.",
      href: `${base}/databases`,
      icon: Database,
    },
    {
      label: "Create storage bucket",
      description: "Store application files and objects.",
      href: `${base}/storage`,
      icon: HardDrive,
    },
  ];

  return (
    <Card className="mb-6 border-cyan-300/20 bg-cyan-300/[0.03]">
      <CardHeader className="flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <CardTitle>Your project is ready</CardTitle>
          <p className="mt-1 max-w-2xl text-xs leading-5 text-slate-500">
            Create a resource, configure or deploy it, then inspect its logs and
            status.
          </p>
        </div>
        <Badge variant="neutral">Quick start</Badge>
      </CardHeader>
      <CardContent>
        <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-4">
          {items.map(({ label, description, href, icon: Icon }) => (
            <Link
              key={href}
              href={href}
              className="group flex items-start gap-3 rounded-lg border border-stealth-border bg-stealth-panel/60 p-3 transition hover:border-cyan-300/30 hover:bg-white/[0.04]"
            >
              <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-cyan-300/10 text-cyan-200">
                <Icon className="size-4" />
              </span>
              <span className="min-w-0 flex-1">
                <span className="block text-sm font-medium text-slate-200 group-hover:text-white">
                  {label}
                </span>
                <span className="mt-0.5 block text-[11px] leading-5 text-slate-600">
                  {description}
                </span>
              </span>
              <ArrowUpRight className="mt-0.5 size-3.5 shrink-0 text-slate-600 group-hover:text-cyan-300" />
            </Link>
          ))}
        </div>
        <p className="mt-4 text-[11px] text-slate-600">
          1. Create a resource · 2. Configure or deploy it · 3. Inspect logs and
          status.
        </p>
      </CardContent>
    </Card>
  );
}

export function ProjectOverviewView({
  projectId,
  organizationId,
}: {
  projectId: string;
  organizationId: string;
}) {
  const project = useProject(projectId);
  const usage = useProjectUsage(projectId);
  const audit = useProjectAudit(projectId);
  const storageBuckets = useStorageBuckets(projectId);
  if (project.error)
    return (
      <ErrorState
        title="Could not load project"
        error={project.error}
        retry={() => project.refetch()}
      />
    );
  if (project.isPending) return <LoadingState rows={6} />;
  const current = project.data?.project;
  const usageData = usage.isSuccess ? usage.data?.usage : undefined;
  const storageData =
    storageBuckets.isSuccess && !storageBuckets.isPlaceholderData
      ? storageBuckets.data
      : undefined;
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const showQuickStart = shouldShowQuickStart(
    storageData?.can_manage,
    usageData,
    storageData?.buckets.length,
  );
  return (
    <>
      <PageHeader
        eyebrow="Project overview"
        title={current?.name ?? "Project"}
        description="A live read of the resources and activity returned by the Stealth API."
        actions={
          <Button asChild variant="outline">
            <Link href={`${base}/services`}>
              <Box className="size-4" /> Open services
            </Link>
          </Button>
        }
      />
      <div className="mb-7 flex flex-wrap items-center gap-3">
        <Badge variant="success">
          <span className="size-1.5 rounded-full bg-current" /> Project loaded
        </Badge>
        <span className="text-xs text-slate-600">
          Project record loaded; usage, activity, and storage summaries load
          independently.
        </span>
        {usage.isSuccess && usageData?.captured_at ? (
          <span className="text-xs text-slate-700">
            Snapshot {formatDate(usageData.captured_at)}
          </span>
        ) : null}
        <ResourceId id={projectId} label="Project ID" />
      </div>
      {showQuickStart ? <QuickStart base={base} /> : null}
      {usage.isError ? (
        <OverviewSectionError
          title="Could not load project usage"
          retry={() => usage.refetch()}
        />
      ) : usage.isPending ? (
        <Card>
          <CardContent className="pt-5">
            <LoadingState rows={2} />
          </CardContent>
        </Card>
      ) : usageData ? (
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          <Metric
            label="Functions"
            value={formatCount(usageData.function_count)}
            icon={FunctionSquare}
            href={`${base}/functions`}
          />
          <Metric
            label="Sites"
            value={formatCount(usageData.site_count)}
            icon={Globe2}
            href={`${base}/sites`}
          />
          <Metric
            label="Databases"
            value={formatCount(usageData.database_count)}
            icon={Database}
            href={`${base}/databases`}
          />
          <Metric
            label="Storage"
            value={formatBytes(usageData.storage_bytes)}
            icon={HardDrive}
            href={`${base}/storage`}
          />
        </div>
      ) : null}
      {storageBuckets.isError ? (
        <div className="mt-3">
          <OverviewSectionError
            title="Could not load storage summary"
            retry={() => storageBuckets.refetch()}
          />
        </div>
      ) : null}
      <div
        className={
          usageData && !usage.isError
            ? "mt-6 grid gap-5 xl:grid-cols-[1.2fr_.8fr]"
            : "mt-6"
        }
      >
        {usageData && !usage.isError ? (
          <Card>
            <CardHeader className="flex-row items-center justify-between">
              <div>
                <CardTitle>Resource posture</CardTitle>
                <p className="mt-1 text-xs text-slate-500">
                  Actual counts and usage from the current snapshot.
                </p>
              </div>
              <History className="size-4 text-slate-600" />
            </CardHeader>
            <CardContent>
              <div className="grid gap-3 sm:grid-cols-2">
                {[
                  [
                    "Function artifact",
                    formatBytes(usageData.function_artifact_bytes),
                    formatBytes(usageData.function_quota_bytes),
                  ],
                  [
                    "Site artifact",
                    formatBytes(usageData.site_artifact_bytes),
                    formatBytes(usageData.site_quota_bytes),
                  ],
                  [
                    "Database rows",
                    formatCount(usageData.database_row_count),
                    "rows",
                  ],
                  [
                    "Application users",
                    formatCount(usageData.application_users),
                    "users",
                  ],
                  [
                    "API requests (30d)",
                    formatCount(usageData.api_request_count_30d),
                    "requests",
                  ],
                  [
                    "Function failures (30d)",
                    formatCount(usageData.function_failure_count_30d),
                    "failures",
                  ],
                ].map(([label, value, note]) => (
                  <div
                    key={label}
                    className="rounded-lg border border-stealth-border bg-black/10 p-3.5"
                  >
                    <p className="text-xs text-slate-500">{label}</p>
                    <div className="mt-2 flex items-end justify-between gap-2">
                      <p className="text-lg font-semibold text-white">
                        {value}
                      </p>
                      <span className="text-[10px] text-slate-600">{note}</span>
                    </div>
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>
        ) : null}
        <Card>
          <CardHeader>
            <CardTitle>Recent activity</CardTitle>
            <p className="mt-1 text-xs text-slate-500">
              Project audit events, newest first.
            </p>
          </CardHeader>
          <CardContent className="space-y-4">
            {audit.isPending ? (
              <LoadingState rows={4} />
            ) : audit.isError ? (
              <OverviewSectionError
                title="Could not load recent activity"
                retry={() => audit.refetch()}
              />
            ) : audit.data?.events?.length ? (
              audit.data.events.slice(0, 6).map((event) => (
                <div key={event.id} className="flex gap-3">
                  <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-cyan-300" />
                  <div className="min-w-0">
                    <p className="truncate text-sm text-slate-200">
                      {event.action}
                    </p>
                    <p className="mt-0.5 text-xs text-slate-600">
                      {event.target_type} · {formatDate(event.created_at)}
                    </p>
                  </div>
                </div>
              ))
            ) : (
              <p className="text-sm text-slate-500">
                No audit events returned yet.
              </p>
            )}
          </CardContent>
        </Card>
      </div>
      <p className="mt-6 flex items-center gap-2 rounded-lg border border-stealth-border bg-white/[0.02] px-3 py-2.5 text-xs leading-5 text-slate-600">
        <Users className="size-3.5 shrink-0" /> Backend does not expose a
        unified project health or deployment feed; usage, activity, and health
        are intentionally limited to measured backend data.
      </p>
    </>
  );
}
