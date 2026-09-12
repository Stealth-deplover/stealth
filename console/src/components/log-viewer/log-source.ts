import { api, unwrap } from "@/api/client";
import type {
  AgentRunLog,
  FunctionBuildLog,
  FunctionExecutionLog,
  SiteBuildLog,
} from "@/api/types";

export type LogLine = {
  sequence: number;
  level: string;
  message: string;
  created_at: string;
};

export type LogPageFetcher = (
  after: number | undefined,
  signal: AbortSignal,
) => Promise<LogLine[]>;

/** The resource identity needed to read one of the Console log streams. */
export type LogSourceContext =
  | {
      kind: "function-build";
      projectId: string;
      functionId: string;
      deploymentId: string;
    }
  | {
      kind: "function-execution";
      projectId: string;
      functionId: string;
      executionId: string;
    }
  | {
      kind: "site-build";
      projectId: string;
      siteId: string;
      deploymentId: string;
    }
  | {
      kind: "agent-run";
      agentId: string;
      runId: string;
    };

/** A log source owns its stable identity and cancellable page loader. */
export type LogSource = {
  readonly key: string;
  readonly fetchPage: LogPageFetcher;
};

type ApiLogLine = Pick<
  FunctionBuildLog | FunctionExecutionLog | SiteBuildLog | AgentRunLog,
  "sequence" | "level" | "message" | "created_at"
>;

const LOG_PAGE_LIMIT = 100;

function mapLogLines(logs: readonly ApiLogLine[]): LogLine[] {
  return logs.map((line) => ({
    sequence: line.sequence,
    level: line.level,
    message: line.message,
    created_at: line.created_at,
  }));
}

function queryFor(after?: number) {
  return after === undefined
    ? { limit: LOG_PAGE_LIMIT }
    : { limit: LOG_PAGE_LIMIT, after };
}

function sourceKey(context: LogSourceContext) {
  switch (context.kind) {
    case "function-build":
      return `${context.kind}:${context.projectId}:${context.functionId}:${context.deploymentId}`;
    case "function-execution":
      return `${context.kind}:${context.projectId}:${context.functionId}:${context.executionId}`;
    case "site-build":
      return `${context.kind}:${context.projectId}:${context.siteId}:${context.deploymentId}`;
    case "agent-run":
      return `${context.kind}:${context.agentId}:${context.runId}`;
  }
}

function createSource(
  context: LogSourceContext,
  fetchPage: LogPageFetcher,
): LogSource {
  return { key: sourceKey(context), fetchPage };
}

export function createLogSource(context: LogSourceContext): LogSource {
  switch (context.kind) {
    case "function-build":
      return createSource(context, async (after, signal) => {
        const result = await api.GET(
          "/v1/projects/{projectID}/functions/{functionID}/deployments/{deploymentID}/logs",
          {
            params: {
              path: {
                projectID: context.projectId,
                functionID: context.functionId,
                deploymentID: context.deploymentId,
              },
              query: queryFor(after),
            },
            signal,
          },
        );
        const data = await unwrap(result);
        return mapLogLines(data?.logs ?? []);
      });

    case "function-execution":
      return createSource(context, async (after, signal) => {
        const result = await api.GET(
          "/v1/projects/{projectID}/functions/{functionID}/executions/{executionID}/logs",
          {
            params: {
              path: {
                projectID: context.projectId,
                functionID: context.functionId,
                executionID: context.executionId,
              },
              query: queryFor(after),
            },
            signal,
          },
        );
        const data = await unwrap(result);
        return mapLogLines(data?.logs ?? []);
      });

    case "site-build":
      return createSource(context, async (after, signal) => {
        const result = await api.GET(
          "/v1/projects/{projectID}/sites/{siteID}/deployments/{deploymentID}/logs",
          {
            params: {
              path: {
                projectID: context.projectId,
                siteID: context.siteId,
                deploymentID: context.deploymentId,
              },
              query: queryFor(after),
            },
            signal,
          },
        );
        const data = await unwrap(result);
        return mapLogLines(data?.logs ?? []);
      });

    case "agent-run":
      return createSource(context, async (after, signal) => {
        const result = await api.GET("/v1/agents/{agentID}/runs/{runID}/logs", {
          params: {
            path: { agentID: context.agentId, runID: context.runId },
            query: queryFor(after),
          },
          signal,
        });
        const data = await unwrap(result);
        return mapLogLines(data?.logs ?? []);
      });
  }
}
