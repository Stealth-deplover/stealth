import { describe, expect, it } from "vitest";
import {
  deploymentDurationMs,
  getDeploymentLifecycleStatus,
  isDeploymentInProgress,
} from "./deployment-state";

describe("deployment lifecycle state", () => {
  it("uses build status while an initial Function build is queued", () => {
    const deployment = { status: "ready", build_status: "queued" };

    expect(getDeploymentLifecycleStatus(deployment)).toBe("queued");
    expect(isDeploymentInProgress(deployment)).toBe(true);
  });

  it("marks failed builds as failed even when the deployment status is stale", () => {
    expect(
      getDeploymentLifecycleStatus({ status: "ready", build_status: "failed" }),
    ).toBe("failed");
    expect(
      isDeploymentInProgress({ status: "failed", build_status: "failed" }),
    ).toBe(false);
  });

  it("calculates duration from build start through finish", () => {
    expect(
      deploymentDurationMs({
        queued_at: "2026-01-01T00:00:00.000Z",
        build_started_at: "2026-01-01T00:00:02.000Z",
        built_at: "2026-01-01T00:00:05.500Z",
      }),
    ).toBe(3500);
  });

  it("does not include active time when a built deployment is superseded", () => {
    expect(
      deploymentDurationMs({
        build_started_at: "2026-01-01T00:00:02.000Z",
        built_at: "2026-01-01T00:00:05.500Z",
        finished_at: "2026-01-02T00:00:00.000Z",
      }),
    ).toBe(3500);
  });

  it.each([
    [{ status: "queued", build_status: "running" }, "building", true],
    [{ status: "active", build_status: "deferred" }, "queued", true],
    [{ status: "ready", build_status: "succeeded" }, "ready", false],
    [{ status: "cancelled", build_status: "queued" }, "cancelled", false],
    [{ status: "superseded", build_status: "queued" }, "superseded", false],
    [{ status: "active", build_status: "failed" }, "failed", false],
    [{}, "unknown", false],
  ])("resolves lifecycle for %j", (deployment, status, inProgress) => {
    expect(getDeploymentLifecycleStatus(deployment)).toBe(status);
    expect(isDeploymentInProgress(deployment)).toBe(inProgress);
  });

  it("uses the finish time when a build fails", () => {
    expect(
      deploymentDurationMs({
        build_started_at: "2026-01-01T00:00:02.000Z",
        finished_at: "2026-01-01T00:00:05.000Z",
      }),
    ).toBe(3000);
  });

  it.each([
    {},
    { queued_at: "2026-01-01T00:00:00.000Z" },
    { build_started_at: "invalid", finished_at: "invalid" },
    {
      build_started_at: "2026-01-01T00:00:05.000Z",
      finished_at: "2026-01-01T00:00:02.000Z",
    },
  ])(
    "leaves duration unknown for incomplete or invalid timing %j",
    (timing) => {
      expect(deploymentDurationMs(timing)).toBeNull();
    },
  );
});
