import { api, unwrap } from "@/api/client";
import type {
  AgentRunLog,
  AppBuildLog,
  FunctionBuildLog,
  FunctionExecutionLog,
  SiteBuildLog,
} from "@/api/types";

export type LogLine = {
  id: string;
  level: string;
  message: string;
  created_at: string;
};

export type LogPage = {
  lines: LogLine[];
  nextCursor?: string;
};

export type LogPageFetcher = (
  cursor: string | undefined,
  signal: AbortSignal,
) => Promise<LogPage>;

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
      kind: "app-build";
      projectId: string;
      appId: string;
      deploymentId: string;
    }
  | {
      kind: "app-runtime";
      projectId: string;
      appId: string;
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
  | FunctionBuildLog
  | FunctionExecutionLog
  | SiteBuildLog
  | AppBuildLog
  | AgentRunLog,
  "sequence" | "level" | "message" | "created_at"
>;

const LOG_PAGE_LIMIT = 100;
const NUMERIC_CURSOR_PREFIX = "seq:";

function mapNumericLogLines(logs: readonly ApiLogLine[]): LogLine[] {
  return logs.map((line) => {
    if (!Number.isSafeInteger(line.sequence) || line.sequence < 0) {
      throw new Error("The log source returned an invalid sequence.");
    }
    return {
      id: `${NUMERIC_CURSOR_PREFIX}${line.sequence}`,
      level: line.level,
      message: line.message,
      created_at: line.created_at,
    };
  });
}

function mapNumericPage(logs: readonly ApiLogLine[], after?: string): LogPage {
  const lines = mapNumericLogLines(logs);
  if (!lines.length) return { lines, nextCursor: after };
  const latest = logs.reduce(
    (sequence, line) => Math.max(sequence, line.sequence),
    0,
  );
  return { lines, nextCursor: `${NUMERIC_CURSOR_PREFIX}${latest}` };
}

function decodeNumericCursor(cursor?: string): number | undefined {
  if (cursor === undefined) return undefined;
  if (!cursor.startsWith(NUMERIC_CURSOR_PREFIX)) {
    throw new Error("The log source cursor is invalid.");
  }
  const sequence = Number(cursor.slice(NUMERIC_CURSOR_PREFIX.length));
  if (!Number.isSafeInteger(sequence) || sequence < 0) {
    throw new Error("The log source cursor is invalid.");
  }
  return sequence;
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
    case "app-build":
      return `${context.kind}:${context.projectId}:${context.appId}:${context.deploymentId}`;
    case "app-runtime":
      return `${context.kind}:${context.projectId}:${context.appId}`;
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
      return createSource(context, async (cursor, signal) => {
        const after = decodeNumericCursor(cursor);
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
        return mapNumericPage(data?.logs ?? [], cursor);
      });

    case "function-execution":
      return createSource(context, async (cursor, signal) => {
        const after = decodeNumericCursor(cursor);
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
        return mapNumericPage(data?.logs ?? [], cursor);
      });

    case "site-build":
      return createSource(context, async (cursor, signal) => {
        const after = decodeNumericCursor(cursor);
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
        return mapNumericPage(data?.logs ?? [], cursor);
      });

    case "app-build":
      return createSource(context, async (cursor, signal) => {
        const after = decodeNumericCursor(cursor);
        const result = await api.GET(
          "/v1/projects/{projectID}/apps/{appID}/deployments/{deploymentID}/logs",
          {
            params: {
              path: {
                projectID: context.projectId,
                appID: context.appId,
                deploymentID: context.deploymentId,
              },
              query: queryFor(after),
            },
            signal,
          },
        );
        const data = await unwrap(result);
        return mapNumericPage(data?.logs ?? [], cursor);
      });

    case "app-runtime":
      return createSource(context, async (cursor, signal) => {
        const result = await api.GET(
          "/v1/projects/{projectID}/apps/{appID}/logs",
          {
            params: {
              path: { projectID: context.projectId, appID: context.appId },
              query:
                cursor === undefined
                  ? { limit: LOG_PAGE_LIMIT }
                  : { limit: LOG_PAGE_LIMIT, cursor },
            },
            signal,
          },
        );
        const data = await unwrap(result);
        return {
          lines: (data?.logs ?? []).map((line) => ({
            id: line.id,
            level: line.level,
            message: line.message,
            created_at: line.created_at,
          })),
          nextCursor: data?.next_cursor ?? cursor,
        };
      });

    case "agent-run":
      return createSource(context, async (cursor, signal) => {
        const after = decodeNumericCursor(cursor);
        const result = await api.GET("/v1/agents/{agentID}/runs/{runID}/logs", {
          params: {
            path: { agentID: context.agentId, runID: context.runId },
            query: queryFor(after),
          },
          signal,
        });
        const data = await unwrap(result);
        return mapNumericPage(data?.logs ?? [], cursor);
      });
  }
}
