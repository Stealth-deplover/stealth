"use client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { type CursorQuery, withCursorPage } from "@/api/pagination";
import { queryKeys } from "@/api/query-keys";

type ListQueryOptions = {
  enabled?: boolean;
};

export function useProjects(
  organizationId: string | undefined,
  query?: CursorQuery,
  options?: ListQueryOptions,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.projects(organizationId ?? ""), params],
    enabled: Boolean(organizationId) && (options?.enabled ?? true),
    queryFn: async () =>
      unwrap(
        await api.GET("/v1/organizations/{organizationID}/projects", {
          params: { path: { organizationID: organizationId! }, query: params },
        }),
      ),
    placeholderData: keepPreviousData,
  });
}

export function useProject(projectId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.project(projectId ?? ""),
    enabled: Boolean(projectId),
    queryFn: async () =>
      unwrap(
        await api.GET("/v1/projects/{projectID}", {
          params: { path: { projectID: projectId! } },
        }),
      ),
  });
}

export function useProjectUsage(projectId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.usage(projectId ?? ""),
    enabled: Boolean(projectId),
    queryFn: async () =>
      unwrap(
        await api.GET("/v1/projects/{projectID}/usage", {
          params: { path: { projectID: projectId! } },
        }),
      ),
  });
}
