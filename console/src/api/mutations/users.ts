"use client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { queryKeys } from "@/api/query-keys";
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
      queryClient.invalidateQueries({ queryKey: queryKeys.users(projectId) }),
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
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.users(projectId) });
      queryClient.invalidateQueries({
        queryKey: queryKeys.projectUser(projectId, userId),
      });
    },
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
      queryClient.invalidateQueries({ queryKey: queryKeys.users(projectId) });
      queryClient.removeQueries({
        queryKey: queryKeys.projectUser(projectId, userId),
      });
    },
  });
}
