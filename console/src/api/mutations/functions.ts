"use client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap, uploadMultipart } from "@/api/client";
import { queryKeys } from "@/api/query-keys";
import type { components } from "@/api/generated/schema";

export function useCreateFunction(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["CreateFunctionRequest"]) =>
      unwrap(
        await api.POST("/v1/projects/{projectID}/functions", {
          params: { path: { projectID: projectId } },
          body,
        }),
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.functions(projectId),
      }),
  });
}

export function useCreateFunctionVariable(
  projectId: string,
  functionId: string,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["CreateFunctionVariableRequest"],
    ) =>
      unwrap(
        await api.POST(
          "/v1/projects/{projectID}/functions/{functionID}/variables",
          {
            params: { path: { projectID: projectId, functionID: functionId } },
            body,
          },
        ),
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.functionVariables(projectId, functionId),
      }),
  });
}

export function useDeleteFunctionVariable(
  projectId: string,
  functionId: string,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (variableId: string) =>
      unwrap(
        await api.DELETE(
          "/v1/projects/{projectID}/functions/{functionID}/variables/{variableID}",
          {
            params: {
              path: {
                projectID: projectId,
                functionID: functionId,
                variableID: variableId,
              },
            },
          },
        ),
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.functionVariables(projectId, functionId),
      }),
  });
}

export function useActivateFunctionDeployment(
  projectId: string,
  functionId: string,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (deploymentId: string) =>
      unwrap(
        await api.POST(
          "/v1/projects/{projectID}/functions/{functionID}/deployments/{deploymentID}/activate",
          {
            params: {
              path: {
                projectID: projectId,
                functionID: functionId,
                deploymentID: deploymentId,
              },
            },
          },
        ),
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.function(projectId, functionId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.functionDeployments(projectId, functionId),
      });
    },
  });
}

export function useUploadFunctionDeployment(
  projectId: string,
  functionId: string,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({ file }: { file: File }) => {
      const form = new FormData();
      form.append("source", file);
      return uploadMultipart(
        `/v1/projects/${projectId}/functions/${functionId}/deployments`,
        form,
      );
    },
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.function(projectId, functionId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.functionDeployments(projectId, functionId),
      });
    },
  });
}
