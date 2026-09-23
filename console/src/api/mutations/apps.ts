"use client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { applyCacheChanges } from "@/api/cache-coherence";
import type { components } from "@/api/generated/schema";

export function useCreateApp(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["CreateAppRequest"]) =>
      unwrap(
        await api.POST("/v1/projects/{projectID}/apps", {
          params: { path: { projectID: projectId } },
          body,
        }),
      ),
    onSuccess: () => applyCacheChanges(queryClient, [{ kind: "app", projectId }]),
  });
}

export function useUpdateApp(projectId: string, appId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["UpdateAppRequest"]) =>
      unwrap(
        await api.PATCH("/v1/projects/{projectID}/apps/{appID}", {
          params: { path: { projectID: projectId, appID: appId } },
          body,
        }),
      ),
    onSuccess: () => applyCacheChanges(queryClient, [{ kind: "app", projectId, appId }]),
  });
}

export function useDeleteApp(projectId: string, appId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () =>
      unwrap(
        await api.DELETE("/v1/projects/{projectID}/apps/{appID}", {
          params: { path: { projectID: projectId, appID: appId } },
        }),
      ),
    onSuccess: () => applyCacheChanges(queryClient, [{ kind: "app", projectId, appId }]),
  });
}
