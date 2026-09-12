"use client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, cancellableQuery, execute } from "@/api/client";
import {
  type AgentListQuery,
  type CursorQuery,
  withAgentPage,
  withCursorPage,
} from "@/api/pagination";
import { queryKeys } from "@/api/query-keys";
import { isAgentRunActive } from "@/features/agents/agent-run-state";

type ProjectAgentQuery = Omit<AgentListQuery, "project_id">;

export function useAgents(
  projectId: string | undefined,
  query?: ProjectAgentQuery,
) {
  const params = withAgentPage({ ...query, project_id: projectId! });
  return useQuery({
    queryKey: [...queryKeys.agents(projectId ?? ""), params],
    enabled: Boolean(projectId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/agents", { params: { query: params }, signal }),
    ),
    placeholderData: keepPreviousData,
  });
}

export function useAgent(agentId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.agent(agentId ?? ""),
    enabled: Boolean(agentId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/agents/{agentID}", {
        params: { path: { agentID: agentId! } },
        signal,
      }),
    ),
  });
}

export function useAgentCatalog() {
  return useQuery({
    queryKey: queryKeys.agentCatalog,
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/agent-catalog", { signal }),
    ),
    staleTime: 300_000,
  });
}

export function useAgentRuns(agentId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.agentRuns(agentId ?? ""), params],
    enabled: Boolean(agentId),
    queryFn: ({ signal }) =>
      execute(
        api.GET("/v1/agents/{agentID}/runs", {
          params: { path: { agentID: agentId! }, query: params },
          signal,
        }),
      ),
    placeholderData: keepPreviousData,
    refetchInterval: (query) =>
      (query.state.data?.runs ?? []).some((run) => isAgentRunActive(run.status))
        ? 1_000
        : false,
  });
}

export function useAgentRun(
  agentId: string | undefined,
  runId: string | undefined,
) {
  return useQuery({
    queryKey: queryKeys.agentRun(agentId ?? "", runId ?? ""),
    enabled: Boolean(agentId && runId),
    queryFn: ({ signal }) =>
      execute(
        api.GET("/v1/agents/{agentID}/runs/{runID}", {
          params: { path: { agentID: agentId!, runID: runId! } },
          signal,
        }),
      ),
    refetchInterval: (query) => {
      const status = query.state.data?.run.status;
      return status && isAgentRunActive(status) ? 1_000 : false;
    },
  });
}
