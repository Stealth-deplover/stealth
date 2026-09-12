"use client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { queryKeys } from "@/api/query-keys";
import { applyCacheChanges } from "@/api/cache-coherence";
import type { components } from "@/api/generated/schema";

export function useCreateProjectUser(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["CreateProjectUserRequest"],
    ) =>
      unwrap(
        await api.POST("/v1/projects/{projectID}/users", {
          params: { path: { projectID: projectId } },
          body,
        }),
      ),
    onSuccess: () =>
      applyCacheChanges(queryClient, [{ kind: "project-user", projectId }]),
  });
}

export function useUpdateProjectUserStatus(projectId: string, userId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["UpdateProjectUserStatusRequest"],
    ) =>
      unwrap(
        await api.PATCH("/v1/projects/{projectID}/users/{userID}/status", {
          params: { path: { projectID: projectId, userID: userId } },
          body,
        }),
      ),
    onSuccess: () =>
      applyCacheChanges(queryClient, [
        { kind: "project-user", projectId, userId },
      ]),
  });
}

export function useDeleteProjectUser(projectId: string, userId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () =>
      unwrap(
        await api.DELETE("/v1/projects/{projectID}/users/{userID}", {
          params: { path: { projectID: projectId, userID: userId } },
        }),
      ),
    onSuccess: () => {
      queryClient.removeQueries({
        queryKey: queryKeys.projectUser(projectId, userId),
      });
      void applyCacheChanges(queryClient, [{ kind: "project-user", projectId }]);
    },
  });
}
