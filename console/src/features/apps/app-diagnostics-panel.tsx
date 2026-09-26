import { formatDate } from "@/lib/format";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { AppDetailMetric } from "@/features/apps/app-detail-metric";
import type { AppDiagnostics } from "@/api/types";

export function AppDiagnosticsPanel({
  diagnostics,
  isPending,
  error,
  isError,
  retry,
}: {
  diagnostics: AppDiagnostics | undefined;
  isPending: boolean;
  error: Error | null;
  isError: boolean;
  retry: () => void;
}) {
  if (isError) {
    return (
      <div className="mb-5">
        <ErrorState
          title="Could not load App diagnostics"
          error={error}
          retry={retry}
        />
      </div>
    );
  }
  if (isPending || !diagnostics) {
    return (
      <Card className="mb-5">
        <CardHeader>
          <CardTitle>Runtime diagnostics</CardTitle>
        </CardHeader>
        <CardContent>
          <LoadingState rows={3} />
        </CardContent>
      </Card>
    );
  }

  return (
    <Card className="mb-5">
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
        <div>
          <CardTitle>Runtime diagnostics</CardTitle>
          <p className="mt-1 text-xs leading-5 text-fog">
            Desired is the release selected in PostgreSQL. Applied is the last
            release confirmed by the runtime worker.
          </p>
        </div>
        <StatusBadge status={diagnostics.convergence_status} />
      </CardHeader>
      <CardContent className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <AppDetailMetric label="Desired release">
          {diagnostics.desired_deployment
            ? `v${diagnostics.desired_deployment.version}`
            : "None"}
        </AppDetailMetric>
        <AppDetailMetric label="Applied release">
          {diagnostics.applied_deployment
            ? `v${diagnostics.applied_deployment.version}`
            : "None"}
        </AppDetailMetric>
        <AppDetailMetric label="Desired generation">
          {diagnostics.desired_generation}
        </AppDetailMetric>
        <AppDetailMetric label="Applied generation">
          {diagnostics.applied_generation ?? "Not applied"}
        </AppDetailMetric>
        <AppDetailMetric label="Runtime">
          <StatusBadge status={diagnostics.runtime_status} />
        </AppDetailMetric>
        <AppDetailMetric label="Health">
          <StatusBadge status={diagnostics.health_status} />
        </AppDetailMetric>
        <AppDetailMetric label="Route">
          <StatusBadge status={diagnostics.route_status} />
        </AppDetailMetric>
        <AppDetailMetric label="Artifact metadata">
          <StatusBadge
            status={
              diagnostics.desired_artifact_ready ? "ready" : "unavailable"
            }
          />
        </AppDetailMetric>
        <AppDetailMetric label="Retry state">
          {diagnostics.failure_count === 0
            ? "No recorded failures"
            : `${diagnostics.failure_count} failed attempt${diagnostics.failure_count === 1 ? "" : "s"}`}
        </AppDetailMetric>
        <AppDetailMetric label="Next retry">
          {formatDate(diagnostics.next_retry_at)}
        </AppDetailMetric>
        <AppDetailMetric label="Last failure">
          {formatDate(diagnostics.last_failure_at)}
        </AppDetailMetric>
        <AppDetailMetric label="Last health check">
          {formatDate(diagnostics.health_checked_at)}
        </AppDetailMetric>
        <AppDetailMetric label="Last transition">
          {formatDate(diagnostics.last_transition_at)}
        </AppDetailMetric>
        <AppDetailMetric label="Last started">
          {formatDate(diagnostics.last_started_at)}
        </AppDetailMetric>
        <AppDetailMetric label="Last stopped">
          {formatDate(diagnostics.last_stopped_at)}
        </AppDetailMetric>
      </CardContent>
      {diagnostics.runtime_error ? (
        <CardContent className="border-t border-white/[0.06] py-3">
          <p className="text-xs font-medium text-coral-red">Runtime detail</p>
          <p className="mt-1 text-sm text-mist">{diagnostics.runtime_error}</p>
        </CardContent>
      ) : null}
      {diagnostics.issues.length ? (
        <CardContent className="border-t border-white/[0.06] py-3">
          <h3 className="text-xs font-medium text-paper">Current issues</h3>
          <ul className="mt-3 space-y-3">
            {diagnostics.issues.map((issue) => (
              <li
                key={issue.code}
                className="flex flex-col gap-1.5 sm:flex-row sm:items-start sm:gap-3"
              >
                <Badge variant={issueSeverityVariant(issue.severity)}>
                  {issue.severity}
                </Badge>
                <div className="min-w-0">
                  <p className="text-sm text-mist">{issue.message}</p>
                  <p className="mt-0.5 font-mono text-[11px] text-fog">
                    {issue.code}
                  </p>
                </div>
              </li>
            ))}
          </ul>
        </CardContent>
      ) : (
        <CardContent className="border-t border-white/[0.06] py-3">
          <p className="text-sm text-mist">No current runtime issues.</p>
        </CardContent>
      )}
    </Card>
  );
}

function issueSeverityVariant(
  severity: string,
): "neutral" | "warning" | "error" {
  if (severity === "error") return "error";
  if (severity === "warning") return "warning";
  return "neutral";
}
