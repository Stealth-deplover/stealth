"use client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, cancellableQuery, unwrap } from "@/api/client";
import { type CursorQuery, withCursorPage } from "@/api/pagination";
import { queryKeys } from "@/api/query-keys";
import { fetchAllCursorPages } from "@/lib/cursor-pagination";

export function useApps(projectId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.apps(projectId ?? ""), params],
    enabled: Boolean(projectId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/apps", {
        params: { path: { projectID: projectId! }, query: params },
        signal,
      }),
    ),
    placeholderData: keepPreviousData,
  });
}

export function useCanvasApps(projectId: string | undefined) {
  return useQuery({
    queryKey: [...queryKeys.apps(projectId ?? ""), "canvas"],
    enabled: Boolean(projectId),
    staleTime: 30_000,
    queryFn: ({ signal }) =>
      fetchAllCursorPages(
        (cursor) =>
          api
            .GET("/v1/projects/{projectID}/apps", {
              params: {
                path: { projectID: projectId! },
                query: withCursorPage(cursor ? { cursor } : undefined),
              },
              signal,
            })
            .then(unwrap),
        (page) => page.apps,
        { signal },
      ),
  });
}

export function useApp(
  projectId: string | undefined,
  appId: string | undefined,
) {
  return useQuery({
    queryKey: queryKeys.app(projectId ?? "", appId ?? ""),
    enabled: Boolean(projectId && appId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/apps/{appID}", {
        params: { path: { projectID: projectId!, appID: appId! } },
        signal,
      }),
    ),
  });
}

export function useAppDiagnostics(
  projectId: string | undefined,
  appId: string | undefined,
) {
  return useQuery({
    queryKey: queryKeys.appDiagnostics(projectId ?? "", appId ?? ""),
    enabled: Boolean(projectId && appId),
    queryFn: async ({ signal }) => {
      const response = await api.GET(
        "/v1/projects/{projectID}/apps/{appID}/diagnostics",
        {
          params: { path: { projectID: projectId!, appID: appId! } },
          signal,
        },
      );
      const diagnostics = await unwrap(response);
      if (!diagnostics)
        throw new Error("The App diagnostics response was empty.");
      return diagnostics;
    },
    refetchInterval: (current) =>
      current.state.data?.convergence_status === "reconciling" ? 2_000 : false,
  });
}

export function useAppDeployments(
  projectId: string | undefined,
  appId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [
      ...queryKeys.appDeployments(projectId ?? "", appId ?? ""),
      params,
    ],
    enabled: Boolean(projectId && appId),
    queryFn: async ({ signal }) => {
      const response = await api.GET(
        "/v1/projects/{projectID}/apps/{appID}/deployments",
        {
          params: {
            path: { projectID: projectId!, appID: appId! },
            query: params,
          },
          signal,
        },
      );
      const page = await unwrap(response);
      if (!page) throw new Error("The App deployment response was empty.");
      return page;
    },
    placeholderData: keepPreviousData,
    refetchInterval: (current) =>
      current.state.data?.deployments.some(
        (deployment) =>
          deployment.status === "queued" || deployment.status === "building",
      )
        ? 2_000
        : false,
  });
}

export function useAppEnvironmentVariables(
  projectId: string | undefined,
  appId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [
      ...queryKeys.appEnvironmentVariables(projectId ?? "", appId ?? ""),
      params,
    ],
    enabled: Boolean(projectId && appId),
    queryFn: async ({ signal }) => {
      const response = await api.GET(
        "/v1/projects/{projectID}/apps/{appID}/variables",
        {
          params: {
            path: { projectID: projectId!, appID: appId! },
            query: params,
          },
          signal,
        },
      );
      const page = await unwrap(response);
      if (!page) throw new Error("The App environment response was empty.");
      return page;
    },
    placeholderData: keepPreviousData,
  });
}
