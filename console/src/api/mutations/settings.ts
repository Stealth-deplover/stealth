"use client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { applyCacheChanges } from "@/api/cache-coherence";
import type { components } from "@/api/generated/schema";

export function useUpdateAuthSettings(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["UpdateProjectAuthSettingsRequest"],
    ) =>
      unwrap(
        await api.PATCH("/v1/projects/{projectID}/auth/settings", {
          params: { path: { projectID: projectId } },
          body,
        }),
      ),
    onSuccess: () =>
      applyCacheChanges(queryClient, [{ kind: "auth-settings", projectId }]),
  });
}

export function useReplaceServiceLayout(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["ReplaceProjectServiceLayoutRequest"],
    ) =>
      unwrap(
        await api.PUT("/v1/projects/{projectID}/service-layout", {
          params: { path: { projectID: projectId } },
          body,
        }),
      ),
    onSuccess: () =>
      applyCacheChanges(queryClient, [{ kind: "service-layout", projectId }]),
  });
}
