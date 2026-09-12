"use client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, cancellableQuery } from "@/api/client";
import { type CursorQuery, withCursorPage } from "@/api/pagination";
import { queryKeys } from "@/api/query-keys";

export function useProjectAPIKeys(
  projectId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.apiKeys(projectId ?? ""), params],
    enabled: Boolean(projectId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/api-keys", {
        params: { path: { projectID: projectId! }, query: params },
        signal,
      }),
    ),
    placeholderData: keepPreviousData,
  });
}

export function useProjectAPIKey(
  projectId: string | undefined,
  keyId: string | undefined,
) {
  return useQuery({
    queryKey: queryKeys.apiKey(projectId ?? "", keyId ?? ""),
    enabled: Boolean(projectId && keyId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/api-keys/{keyID}", {
        params: { path: { projectID: projectId!, keyID: keyId! } },
        signal,
      }),
    ),
  });
}
