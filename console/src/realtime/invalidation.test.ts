import { describe, expect, it } from "vitest";
import {
  realtimeInvalidationKeys,
  type RealtimeNotification,
} from "@/realtime/invalidation";

describe("realtime query invalidation", () => {
  it("invalidates an Agent run list and detail without treating the event as state", () => {
    const event: RealtimeNotification = {
      type: "agent.run.updated",
      resource_id: "run-1",
      payload: { agent_id: "agent-1", status: "running" },
    };
    expect(realtimeInvalidationKeys("project-1", event)).toEqual([
      ["agent-runs", "agent-1"],
      ["agent-run", "agent-1", "run-1"],
    ]);
  });

  it("invalidates deployment queries using the resource metadata", () => {
    expect(
      realtimeInvalidationKeys("project-1", {
        event: "function_deployment.updated",
        resource_id: "deployment-1",
        payload: { function_id: "function-1" },
      }),
    ).toEqual([
      ["function", "project-1", "function-1"],
      ["function-deployments", "project-1", "function-1"],
      ["function-deployment", "project-1", "function-1", "deployment-1"],
    ]);
  });
});
