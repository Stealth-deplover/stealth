"use client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, cancellableQuery, execute, unwrap } from "@/api/client";
import { type CursorQuery, withCursorPage } from "@/api/pagination";
import { queryKeys } from "@/api/query-keys";
import { fetchAllCursorPages } from "@/lib/cursor-pagination";
import { isDeploymentInProgress } from "@/lib/deployment-state";

export function useFunctions(
  projectId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.functions(projectId ?? ""), params],
    enabled: Boolean(projectId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/functions", {
        params: { path: { projectID: projectId! }, query: params },
        signal,
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
    queryFn: ({ signal }) =>
      fetchAllCursorPages(
        (cursor) =>
          api
            .GET("/v1/projects/{projectID}/functions", {
              params: {
                path: { projectID: projectId! },
                query: withCursorPage(cursor ? { cursor } : undefined),
              },
              signal,
            })
            .then(unwrap),
        (page) => page.functions,
        { signal },
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
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/functions/{functionID}", {
        params: { path: { projectID: projectId!, functionID: functionId! } },
        signal,
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
    queryFn: ({ signal }) =>
      execute(
        api.GET("/v1/projects/{projectID}/functions/{functionID}/deployments", {
          params: {
            path: { projectID: projectId!, functionID: functionId! },
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
    queryFn: ({ signal }) =>
      execute(
        api.GET("/v1/projects/{projectID}/functions/{functionID}/executions", {
          params: {
            path: { projectID: projectId!, functionID: functionId! },
            query: params,
          },
          signal,
        }),
      ),
    placeholderData: keepPreviousData,
    refetchInterval: (query) =>
      query.state.data?.executions.some(
        (execution) =>
          execution.status === "accepted" || execution.status === "running",
      )
        ? 3_000
        : false,
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
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/functions/{functionID}/variables", {
        params: {
          path: { projectID: projectId!, functionID: functionId! },
          query: params,
        },
        signal,
      }),
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
    queryFn: ({ signal }) =>
      execute(
        api.GET(
          "/v1/projects/{projectID}/functions/{functionID}/deployments/{deploymentID}",
          {
            params: {
              path: {
                projectID: projectId!,
                functionID: functionId!,
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

export function useFunctionExecution(
  projectId: string | undefined,
  functionId: string | undefined,
  executionId: string | undefined,
) {
  return useQuery({
    queryKey: queryKeys.functionExecution(
      projectId ?? "",
      functionId ?? "",
      executionId ?? "",
    ),
    enabled: Boolean(projectId && functionId && executionId),
    queryFn: ({ signal }) =>
      execute(
        api.GET(
          "/v1/projects/{projectID}/functions/{functionID}/executions/{executionID}",
          {
            params: {
              path: {
                projectID: projectId!,
                functionID: functionId!,
                executionID: executionId!,
              },
            },
            signal,
          },
        ),
      ),
    refetchInterval: (query) => {
      const status = query.state.data?.execution.status;
      return status === "accepted" || status === "running" ? 3_000 : false;
    },
  });
}
