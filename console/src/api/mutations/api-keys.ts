"use client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { applyCacheChanges } from "@/api/cache-coherence";
import type { components } from "@/api/generated/schema";

export function useCreateAPIKey(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["CreateProjectAPIKeyRequest"],
    ) =>
      unwrap(
        await api.POST("/v1/projects/{projectID}/api-keys", {
          params: { path: { projectID: projectId } },
          body,
        }),
      ),
    onSuccess: () =>
      applyCacheChanges(queryClient, [{ kind: "api-key", projectId }]),
  });
}

export function useRevokeAPIKey(projectId: string, keyId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () =>
      unwrap(
        await api.DELETE("/v1/projects/{projectID}/api-keys/{keyID}", {
          params: { path: { projectID: projectId, keyID: keyId } },
        }),
      ),
    onSuccess: () =>
      applyCacheChanges(queryClient, [{ kind: "api-key", projectId, keyId }]),
  });
}
