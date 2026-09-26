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

  it("maps App desired-state changes to the list and detail queries", () => {
    expect(
      invalidationKeysFor({
        kind: "app",
        projectId: "project-1",
        appId: "app-1",
      }),
    ).toEqual([
      ["apps", "project-1"],
      ["app", "project-1", "app-1"],
      ["app-diagnostics", "project-1", "app-1"],
    ]);
  });

  it("refreshes App state and variable metadata after an environment change", () => {
    expect(
      invalidationKeysFor({
        kind: "app-environment-variable",
        projectId: "project-1",
        appId: "app-1",
      }),
    ).toEqual([
      ["app", "project-1", "app-1"],
      ["app-diagnostics", "project-1", "app-1"],
      ["app-environment-variables", "project-1", "app-1"],
    ]);
  });

  it("refreshes diagnostics after a deployment selection or rollback", () => {
    expect(
      invalidationKeysFor({
        kind: "app-deployment",
        projectId: "project-1",
        appId: "app-1",
        deploymentId: "deployment-3",
      }),
    ).toEqual([
      ["apps", "project-1"],
      ["app", "project-1", "app-1"],
      ["app-diagnostics", "project-1", "app-1"],
      ["app-deployments", "project-1", "app-1"],
      ["app-deployment", "project-1", "app-1", "deployment-3"],
    ]);
  });

  it("refreshes the parent Agent when a run can change its derived status", () => {
    expect(
      invalidationKeysFor({
        kind: "agent-run",
        projectId: "project-1",
        agentId: "agent-1",
        includeAgent: true,
        includeAgentDetail: true,
      }),
    ).toEqual([
      ["agent-runs", "agent-1"],
      ["agents", "project-1"],
      ["agent", "agent-1"],
    ]);
  });

  it("maps an authenticated account refresh to all affected global keys", () => {
    expect(
      invalidationKeysFor({
        kind: "account",
        includeOrganizations: true,
        includeBootstrapStatus: true,
      }),
    ).toEqual([["account"], ["organizations"], ["bootstrap-status"]]);
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

  it("refreshes indexes when a table schema changes", () => {
    expect(
      invalidationKeysFor({
        kind: "database-table-schema",
        projectId: "project-1",
        databaseId: "database-1",
        tableId: "table-1",
      }),
    ).toEqual([
      ["columns", "project-1", "database-1", "table-1"],
      ["rows", "project-1", "database-1", "table-1"],
      ["row", "project-1", "database-1", "table-1"],
      ["indexes", "project-1", "database-1", "table-1"],
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
