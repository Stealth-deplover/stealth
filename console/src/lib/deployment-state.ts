export type DeploymentProgress = {
  status?: string | null;
  build_status?: string | null;
};

/**
 * The API exposes a deployment status and, for asynchronous builds, a
 * separate build status. Build status is authoritative while a build is in
 * flight; for example, a Function is initially returned as ready/queued.
 */
export function getDeploymentLifecycleStatus(
  deployment: DeploymentProgress,
): string {
  const status = deployment.status?.toLowerCase();
  const buildStatus = deployment.build_status?.toLowerCase();

  if (status === "failed" || buildStatus === "failed") return "failed";
  if (
    status === "cancelled" ||
    status === "canceled" ||
    status === "superseded"
  )
    return status;
  if (status === "building" || buildStatus === "running") return "building";
  if (
    status === "queued" ||
    buildStatus === "queued" ||
    buildStatus === "deferred"
  )
    return "queued";
  return status ?? "unknown";
}

export function isDeploymentInProgress(
  deployment: DeploymentProgress,
): boolean {
  const lifecycle = getDeploymentLifecycleStatus(deployment);
  return lifecycle === "queued" || lifecycle === "building";
}

export type DeploymentTiming = {
  queued_at?: string | null;
  build_started_at?: string | null;
  built_at?: string | null;
  finished_at?: string | null;
};

export function deploymentDurationMs(
  deployment: DeploymentTiming,
): number | null {
  const started = deployment.build_started_at ?? deployment.queued_at;
  const finished = deployment.built_at ?? deployment.finished_at;
  if (!started || !finished) return null;

  const duration = new Date(finished).valueOf() - new Date(started).valueOf();
  return Number.isFinite(duration) && duration >= 0 ? duration : null;
}
