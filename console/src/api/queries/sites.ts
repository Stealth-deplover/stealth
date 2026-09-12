"use client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, cancellableQuery, execute, unwrap } from "@/api/client";
import { type CursorQuery, withCursorPage } from "@/api/pagination";
import { queryKeys } from "@/api/query-keys";
import { fetchAllCursorPages } from "@/lib/cursor-pagination";
import { isDeploymentInProgress } from "@/lib/deployment-state";

export function useSites(projectId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.sites(projectId ?? ""), params],
    enabled: Boolean(projectId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/sites", {
        params: { path: { projectID: projectId! }, query: params },
        signal,
      }),
    ),
    placeholderData: keepPreviousData,
  });
}

export function useCanvasSites(projectId: string | undefined) {
  return useQuery({
    queryKey: [...queryKeys.sites(projectId ?? ""), "canvas"],
    enabled: Boolean(projectId),
    staleTime: 30_000,
    queryFn: ({ signal }) =>
      fetchAllCursorPages(
        (cursor) =>
          api
            .GET("/v1/projects/{projectID}/sites", {
              params: {
                path: { projectID: projectId! },
                query: withCursorPage(cursor ? { cursor } : undefined),
              },
              signal,
            })
            .then(unwrap),
        (page) => page.sites,
        { signal },
      ),
  });
}

export function useSite(
  projectId: string | undefined,
  siteId: string | undefined,
) {
  return useQuery({
    queryKey: queryKeys.site(projectId ?? "", siteId ?? ""),
    enabled: Boolean(projectId && siteId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/sites/{siteID}", {
        params: { path: { projectID: projectId!, siteID: siteId! } },
        signal,
      }),
    ),
  });
}

export function useSiteDeployments(
  projectId: string | undefined,
  siteId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [
      ...queryKeys.siteDeployments(projectId ?? "", siteId ?? ""),
      params,
    ],
    enabled: Boolean(projectId && siteId),
    queryFn: ({ signal }) =>
      execute(
        api.GET("/v1/projects/{projectID}/sites/{siteID}/deployments", {
          params: {
            path: { projectID: projectId!, siteID: siteId! },
            query: params,
          },
          signal,
        }),
      ),
    placeholderData: keepPreviousData,
    refetchInterval: (query) =>
      query.state.data?.deployments.some(isDeploymentInProgress)
        ? 3_000
        : false,
  });
}

export function useSiteDeployment(
  projectId: string | undefined,
  siteId: string | undefined,
  deploymentId: string | undefined,
) {
  return useQuery({
    queryKey: queryKeys.siteDeployment(
      projectId ?? "",
      siteId ?? "",
      deploymentId ?? "",
    ),
    enabled: Boolean(projectId && siteId && deploymentId),
    queryFn: ({ signal }) =>
      execute(
        api.GET(
          "/v1/projects/{projectID}/sites/{siteID}/deployments/{deploymentID}",
          {
            params: {
              path: {
                projectID: projectId!,
                siteID: siteId!,
                deploymentID: deploymentId!,
              },
            },
            signal,
          },
        ),
      ),
    refetchInterval: (query) => {
      const deployment = query.state.data?.deployment;
      return deployment && isDeploymentInProgress(deployment) ? 3_000 : false;
    },
  });
}
