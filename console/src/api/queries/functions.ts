"use client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { type CursorQuery, withCursorPage } from "@/api/pagination";
import { queryKeys } from "@/api/query-keys";
import { fetchAllCursorPages } from "@/lib/cursor-pagination";

export function useFunctions(
  projectId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.functions(projectId ?? ""), params],
    enabled: Boolean(projectId),
    queryFn: async () =>
      unwrap(
        await api.GET("/v1/projects/{projectID}/functions", {
          params: { path: { projectID: projectId! }, query: params },
        }),
      ),
    placeholderData: keepPreviousData,
  });
}

/**
 * The Services canvas is an intentional exception to table pagination: it needs
 * every resource node to render a truthful topology. It follows the API cursor
 * until exhaustion instead of requesting an arbitrary large first page.
 */

export function useCanvasFunctions(projectId: string | undefined) {
  return useQuery({
    queryKey: [...queryKeys.functions(projectId ?? ""), "canvas"],
    enabled: Boolean(projectId),
    staleTime: 30_000,
    queryFn: () =>
      fetchAllCursorPages(
        (cursor) =>
          api
            .GET("/v1/projects/{projectID}/functions", {
              params: {
                path: { projectID: projectId! },
                query: withCursorPage(cursor ? { cursor } : undefined),
              },
            })
            .then(unwrap),
        (page) => page.functions,
      ),
  });
}

export function useFunction(
  projectId: string | undefined,
  functionId: string | undefined,
) {
  return useQuery({
    queryKey: queryKeys.function(projectId ?? "", functionId ?? ""),
    enabled: Boolean(projectId && functionId),
    queryFn: async () =>
      unwrap(
        await api.GET("/v1/projects/{projectID}/functions/{functionID}", {
          params: { path: { projectID: projectId!, functionID: functionId! } },
        }),
      ),
  });
}

export function useFunctionDeployments(
  projectId: string | undefined,
  functionId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [
      ...queryKeys.functionDeployments(projectId ?? "", functionId ?? ""),
      params,
    ],
    enabled: Boolean(projectId && functionId),
    queryFn: async () =>
      unwrap(
        await api.GET(
          "/v1/projects/{projectID}/functions/{functionID}/deployments",
          {
            params: {
              path: { projectID: projectId!, functionID: functionId! },
              query: params,
            },
          },
        ),
      ),
    placeholderData: keepPreviousData,
  });
}

export function useFunctionExecutions(
  projectId: string | undefined,
  functionId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [
      ...queryKeys.functionExecutions(projectId ?? "", functionId ?? ""),
      params,
    ],
    enabled: Boolean(projectId && functionId),
    queryFn: async () =>
      unwrap(
        await api.GET(
          "/v1/projects/{projectID}/functions/{functionID}/executions",
          {
            params: {
              path: { projectID: projectId!, functionID: functionId! },
              query: params,
            },
          },
        ),
      ),
    placeholderData: keepPreviousData,
  });
}

export function useFunctionVariables(
  projectId: string | undefined,
  functionId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [
      ...queryKeys.functionVariables(projectId ?? "", functionId ?? ""),
      params,
    ],
    enabled: Boolean(projectId && functionId),
    queryFn: async () =>
      unwrap(
        await api.GET(
          "/v1/projects/{projectID}/functions/{functionID}/variables",
          {
            params: {
              path: { projectID: projectId!, functionID: functionId! },
              query: params,
            },
          },
        ),
      ),
    placeholderData: keepPreviousData,
  });
}

export function useFunctionDeployment(
  projectId: string | undefined,
  functionId: string | undefined,
  deploymentId: string | undefined,
) {
  return useQuery({
    queryKey: queryKeys.functionDeployment(
      projectId ?? "",
      functionId ?? "",
      deploymentId ?? "",
    ),
    enabled: Boolean(projectId && functionId && deploymentId),
    queryFn: async () =>
      unwrap(
        await api.GET(
          "/v1/projects/{projectID}/functions/{functionID}/deployments/{deploymentID}",
          {
            params: {
              path: {
                projectID: projectId!,
                functionID: functionId!,
                deploymentID: deploymentId!,
              },
            },
          },
        ),
      ),
    refetchInterval: (query) => {
      const deployment = query.state.data?.deployment;
      return deployment &&
        (deployment.status === "queued" ||
          deployment.build_status === "running" ||
          deployment.build_status === "queued")
        ? 3_000
        : false;
    },
  });
}
