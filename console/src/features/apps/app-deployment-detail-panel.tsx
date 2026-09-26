import { useMemo } from "react";
import { createLogSource, LogViewer } from "@/components/log-viewer";
import { formatBytes, formatDate } from "@/lib/format";
import { StatusBadge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import type { AppDeployment, StealthApp } from "@/api/types";
import { AppDetailMetric } from "@/features/apps/app-detail-metric";

export function AppDeploymentDetailPanel({
  app,
  appId,
  projectId,
  deployment,
}: {
  app: StealthApp;
  appId: string;
  projectId: string;
  deployment: AppDeployment | undefined;
}) {
  const deploymentId = deployment?.id;
  const buildLogSource = useMemo(
    () =>
      deploymentId
        ? createLogSource({
            kind: "app-build",
            projectId,
            appId,
            deploymentId,
          })
        : null,
    [appId, deploymentId, projectId],
  );

  return (
    <>
      {deployment ? (
        <div className="mt-5 space-y-4">
          <Card>
            <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
              <div>
                <CardTitle>
                  App build deployment v{deployment.version}
                </CardTitle>
                <p className="mt-1 text-xs text-fog">
                  Created {formatDate(deployment.created_at)} ·{" "}
                  {deployment.source_name ?? "Source archive"}
                </p>
              </div>
              <StatusBadge status={deployment.build_status} />
            </CardHeader>
            <CardContent className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
              <AppDetailMetric label="Source SHA-256">
                <span className="break-all font-mono text-[11px]">
                  {deployment.source_checksum_sha256}
                </span>
              </AppDetailMetric>
              <AppDetailMetric label="Dockerfile">
                {deployment.dockerfile_path}
              </AppDetailMetric>
              <AppDetailMetric label="Context directory">
                {deployment.context_directory}
              </AppDetailMetric>
              <AppDetailMetric label="Target">
                {deployment.target ?? "Default stage"}
              </AppDetailMetric>
              <AppDetailMetric label="Platform">
                {deployment.platform}
              </AppDetailMetric>
              <AppDetailMetric label="WorkloadSpec snapshot">
                {deployment.workload_snapshot.schema_version} ·{" "}
                {deployment.workload_spec_sha256.slice(0, 16)}…
              </AppDetailMetric>
              <AppDetailMetric label="Image digest">
                <span className="break-all font-mono text-[11px]">
                  {deployment.image_digest ?? "Not produced"}
                </span>
              </AppDetailMetric>
              <AppDetailMetric label="OCI archive SHA-256">
                <span className="break-all font-mono text-[11px]">
                  {deployment.image_archive_sha256 ?? "Not produced"}
                </span>
              </AppDetailMetric>
              <AppDetailMetric label="Image archive size">
                {deployment.image_size_bytes
                  ? formatBytes(deployment.image_size_bytes)
                  : "Not produced"}
              </AppDetailMetric>
              <AppDetailMetric label="Selection">
                {app.desired_deployment_id === deployment.id
                  ? "Desired image selected"
                  : "Not selected"}
              </AppDetailMetric>
              <AppDetailMetric label="Build started">
                {deployment.build_started_at
                  ? formatDate(deployment.build_started_at)
                  : "Not started"}
              </AppDetailMetric>
              <AppDetailMetric label="Built">
                {deployment.built_at
                  ? formatDate(deployment.built_at)
                  : "Not built"}
              </AppDetailMetric>
              {deployment.error_message ? (
                <AppDetailMetric label="Build error">
                  {deployment.error_message}
                </AppDetailMetric>
              ) : null}
            </CardContent>
          </Card>
          <LogViewer
            key={deployment.id}
            title="Build logs"
            description="Bounded output from the trusted App build worker."
            source={buildLogSource}
            polling={
              deployment.status === "queued" || deployment.status === "building"
            }
            emptyMessage="Build output will appear when this deployment starts."
          />
        </div>
      ) : null}
    </>
  );
}
