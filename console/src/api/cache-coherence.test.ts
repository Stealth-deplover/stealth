import { describe, expect, it, vi } from "vitest";
import { applyCacheChanges, invalidationKeysFor } from "@/api/cache-coherence";

describe("cache coherence", () => {
  it("maps an Agent run change to its list and detail keys", () => {
    expect(
      invalidationKeysFor({
        kind: "agent-run",
        projectId: "project-1",
        agentId: "agent-1",
        runId: "run-1",
      }),
    ).toEqual([
      ["agent-runs", "agent-1"],
      ["agent-run", "agent-1", "run-1"],
    ]);
  });

  it("maps a function deployment change to the affected resource keys", () => {
    expect(
      invalidationKeysFor({
        kind: "function-deployment",
        projectId: "project-1",
        functionId: "function-1",
        deploymentId: "deployment-1",
      }),
    ).toEqual([
      ["function", "project-1", "function-1"],
      ["function-deployments", "project-1", "function-1"],
      ["function-deployment", "project-1", "function-1", "deployment-1"],
    ]);
  });

  it("maps an authenticated account refresh to all affected global keys", () => {
    expect(
      invalidationKeysFor({
        kind: "account",
        includeOrganizations: true,
        includeBootstrapStatus: true,
      }),
    ).toEqual([
      ["account"],
      ["organizations"],
      ["bootstrap-status"],
    ]);
  });

  it("keeps database restore invalidation policy in one module", () => {
    expect(
      invalidationKeysFor({
        kind: "database-backup",
        projectId: "project-1",
        databaseId: "database-1",
        restore: true,
      }),
    ).toEqual([
      ["rows", "project-1", "database-1"],
      ["row", "project-1", "database-1"],
      ["table", "project-1", "database-1"],
      ["columns", "project-1", "database-1"],
      ["indexes", "project-1", "database-1"],
      ["database", "project-1", "database-1"],
      ["tables", "project-1", "database-1"],
      ["database-backups", "project-1", "database-1"],
    ]);
  });

  it("maps storage file changes without leaking query keys to callers", () => {
    expect(
      invalidationKeysFor({
        kind: "storage-file",
        projectId: "project-1",
        bucketId: "bucket-1",
        operation: "rename",
      }),
    ).toEqual([
      ["files", "project-1", "bucket-1"],
      ["file", "project-1", "bucket-1"],
    ]);
  });

  it("applies every unique invalidation key exactly once", async () => {
    const invalidateQueries = vi.fn().mockResolvedValue(undefined);
    const queryClient = { invalidateQueries };
    const change = {
      kind: "database" as const,
      projectId: "project-1",
    };

    await applyCacheChanges(queryClient, [change, change]);

    expect(invalidateQueries).toHaveBeenCalledOnce();
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: ["databases", "project-1"],
    });
  });
});
