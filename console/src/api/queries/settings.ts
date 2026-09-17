"use client";
import { useQuery } from "@tanstack/react-query";
import { api, cancellableQuery } from "@/api/client";
import { queryKeys } from "@/api/query-keys";

export function useAuthSettings(projectId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.authSettings(projectId ?? ""),
    enabled: Boolean(projectId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/auth/settings", {
        params: { path: { projectID: projectId! } },
        signal,
      }),
    ),
  });
}

export function useServiceLayout(projectId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.serviceLayout(projectId ?? ""),
    enabled: Boolean(projectId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/service-layout", {
        params: { path: { projectID: projectId! } },
        signal,
      }),
    ),
  });
}
