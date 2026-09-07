"use client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { queryKeys } from "@/api/query-keys";
import type { components } from "@/api/generated/schema";

export function useCreateAgent(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["CreateAgentRequest"]) =>
      unwrap(await api.POST("/v1/agents", { body })),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: queryKeys.agents(projectId) }),
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
      queryClient.invalidateQueries({ queryKey: queryKeys.agentRuns(agentId) }),
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
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.agentRun(agentId, runId),
      });
      queryClient.invalidateQueries({ queryKey: queryKeys.agentRuns(agentId) });
    },
  });
}
