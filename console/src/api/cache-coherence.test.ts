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
