"use client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { queryKeys } from "@/api/query-keys";
import { applyCacheChanges } from "@/api/cache-coherence";
import type { components } from "@/api/generated/schema";

export function useCreateAgent(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["CreateAgentRequest"]) =>
      unwrap(await api.POST("/v1/agents", { body })),
    onSuccess: () =>
      applyCacheChanges(queryClient, [{ kind: "agent", projectId }]),
  });
}

export function useUpdateAgent(projectId: string, agentId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["UpdateAgentRequest"]) =>
      unwrap(
        await api.PATCH("/v1/agents/{agentID}", {
          params: { path: { agentID: agentId } },
          body,
        }),
      ),
    onSuccess: () =>
      applyCacheChanges(queryClient, [{ kind: "agent", projectId, agentId }]),
  });
}

export function useDeleteAgent(projectId: string, agentId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () =>
      unwrap(
        await api.DELETE("/v1/agents/{agentID}", {
          params: { path: { agentID: agentId } },
        }),
      ),
    onSuccess: () => {
      queryClient.removeQueries({ queryKey: queryKeys.agent(agentId) });
      queryClient.removeQueries({ queryKey: queryKeys.agentRuns(agentId) });
      void applyCacheChanges(queryClient, [{ kind: "agent", projectId }]);
    },
  });
}

export function useCreateAgentRun(agentId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["CreateAgentRunRequest"]) =>
      unwrap(
        await api.POST("/v1/agents/{agentID}/runs", {
          params: { path: { agentID: agentId } },
          body,
        }),
      ),
    onSuccess: () =>
      applyCacheChanges(queryClient, [{ kind: "agent-run", agentId }]),
  });
}

export function useCancelAgentRun(agentId: string, runId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () =>
      unwrap(
        await api.POST("/v1/agents/{agentID}/runs/{runID}/cancel", {
          params: { path: { agentID: agentId, runID: runId } },
        }),
      ),
    onSuccess: () =>
      applyCacheChanges(queryClient, [
        {
          kind: "agent-run",
          agentId,
          runId,
          includeAgentDetail: true,
        },
      ]),
  });
}
