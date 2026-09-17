"use client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, cancellableQuery } from "@/api/client";
import { type CursorQuery, withCursorPage } from "@/api/pagination";
import { queryKeys } from "@/api/query-keys";

export function useProjectUsers(
  projectId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.users(projectId ?? ""), params],
    enabled: Boolean(projectId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/users", {
        params: { path: { projectID: projectId! }, query: params },
        signal,
      }),
    ),
    placeholderData: keepPreviousData,
  });
}

export function useProjectUser(
  projectId: string | undefined,
  userId: string | undefined,
) {
  return useQuery({
    queryKey: queryKeys.projectUser(projectId ?? "", userId ?? ""),
    enabled: Boolean(projectId && userId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/users/{userID}", {
        params: { path: { projectID: projectId!, userID: userId! } },
        signal,
      }),
    ),
  });
}
