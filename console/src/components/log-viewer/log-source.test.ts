import { afterEach, describe, expect, it, vi } from "vitest";
import { createLogSource } from "./log-source";

const get = vi.hoisted(() => vi.fn());

vi.mock("@/api/client", () => ({
  api: { GET: get },
  unwrap: async (result: { data?: unknown }) => result.data,
}));

afterEach(() => {
  get.mockReset();
});

describe("createLogSource", () => {
  it("uses a bounded first-page query", async () => {
    get.mockResolvedValueOnce({ data: { logs: [] } });
    const source = createLogSource({
      kind: "agent-run",
      agentId: "agent-1",
      runId: "run-1",
    });
    const signal = new AbortController().signal;

    await expect(source.fetchPage(undefined, signal)).resolves.toEqual({ lines: [], nextCursor: undefined });
    expect(get).toHaveBeenCalledWith(
      "/v1/agents/{agentID}/runs/{runID}/logs",
      expect.objectContaining({
        params: {
          path: { agentID: "agent-1", runID: "run-1" },
          query: { limit: 100 },
        },
        signal,
      }),
    );
  });

  it.each([
    {
      kind: "function-build" as const,
      path: "/v1/projects/{projectID}/functions/{functionID}/deployments/{deploymentID}/logs",
      context: {
        kind: "function-build" as const,
        projectId: "project-1",
        functionId: "function-1",
        deploymentId: "deployment-1",
      },
      pathParams: {
        projectID: "project-1",
        functionID: "function-1",
        deploymentID: "deployment-1",
      },
    },
    {
      kind: "function-execution" as const,
      path: "/v1/projects/{projectID}/functions/{functionID}/executions/{executionID}/logs",
      context: {
        kind: "function-execution" as const,
        projectId: "project-1",
        functionId: "function-1",
        executionId: "execution-1",
      },
      pathParams: {
        projectID: "project-1",
        functionID: "function-1",
        executionID: "execution-1",
      },
    },
    {
      kind: "site-build" as const,
      path: "/v1/projects/{projectID}/sites/{siteID}/deployments/{deploymentID}/logs",
      context: {
        kind: "site-build" as const,
        projectId: "project-1",
        siteId: "site-1",
        deploymentId: "deployment-1",
      },
      pathParams: {
        projectID: "project-1",
        siteID: "site-1",
        deploymentID: "deployment-1",
      },
    },
    {
      kind: "agent-run" as const,
      path: "/v1/agents/{agentID}/runs/{runID}/logs",
      context: {
        kind: "agent-run" as const,
        agentId: "agent-1",
        runId: "run-1",
      },
      pathParams: { agentID: "agent-1", runID: "run-1" },
    },
    {
      kind: "app-build" as const,
      path: "/v1/projects/{projectID}/apps/{appID}/deployments/{deploymentID}/logs",
      context: {
        kind: "app-build" as const,
        projectId: "project-1",
        appId: "app-1",
        deploymentId: "deployment-1",
      },
      pathParams: { projectID: "project-1", appID: "app-1", deploymentID: "deployment-1" },
    },
  ])(
    "loads and maps the $kind endpoint",
    async ({ context, path, pathParams }) => {
      get.mockResolvedValueOnce({
        data: {
          logs: [
            {
              sequence: 7,
              level: "info",
              message: "worker started",
              created_at: "2026-09-12T00:00:00Z",
              unrelated: "not exposed",
            },
          ],
        },
      });
      const source = createLogSource(context);
      const signal = new AbortController().signal;

      await expect(source.fetchPage("seq:6", signal)).resolves.toEqual({
        lines: [{
          id: "seq:7",
          level: "info",
          message: "worker started",
          created_at: "2026-09-12T00:00:00Z",
        }],
        nextCursor: "seq:7",
      });
      expect(source.key).toContain(context.kind);
      expect(get).toHaveBeenCalledWith(
        path,
        expect.objectContaining({
          params: {
            path: pathParams,
            query: { limit: 100, after: 6 },
          },
          signal,
        }),
      );
    },
  );

  it("passes App runtime cursors through without decoding them", async () => {
    get.mockResolvedValueOnce({
      data: {
        logs: [{ id: "opaque-line", level: "WARN", message: "runtime output", created_at: "2026-09-12T00:00:00Z" }],
        next_cursor: "opaque-cursor-token-that-is-not-numeric",
      },
    });
    const source = createLogSource({ kind: "app-runtime", projectId: "project-1", appId: "app-1" });
    const signal = new AbortController().signal;

    await expect(source.fetchPage("opaque-input-cursor", signal)).resolves.toEqual({
      lines: [{ id: "opaque-line", level: "WARN", message: "runtime output", created_at: "2026-09-12T00:00:00Z" }],
      nextCursor: "opaque-cursor-token-that-is-not-numeric",
    });
    expect(get).toHaveBeenCalledWith(
      "/v1/projects/{projectID}/apps/{appID}/logs",
      expect.objectContaining({
        params: {
          path: { projectID: "project-1", appID: "app-1" },
          query: { limit: 100, cursor: "opaque-input-cursor" },
        },
        signal,
      }),
    );
  });

  it("uses a stable key for the same resource and changes it for another", () => {
    const first = createLogSource({
      kind: "function-build",
      projectId: "project-1",
      functionId: "function-1",
      deploymentId: "deployment-1",
    });
    const same = createLogSource({
      kind: "function-build",
      projectId: "project-1",
      functionId: "function-1",
      deploymentId: "deployment-1",
    });
    const other = createLogSource({
      kind: "function-build",
      projectId: "project-1",
      functionId: "function-1",
      deploymentId: "deployment-2",
    });

    expect(same.key).toBe(first.key);
    expect(other.key).not.toBe(first.key);
  });
});
