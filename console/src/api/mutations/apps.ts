"use client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap, uploadMultipart } from "@/api/client";
import { applyCacheChanges } from "@/api/cache-coherence";
import type { components } from "@/api/generated/schema";

export function useCreateApp(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["CreateAppRequest"]) =>
      unwrap(
        await api.POST("/v1/projects/{projectID}/apps", {
          params: { path: { projectID: projectId } },
          body,
        }),
      ),
    onSuccess: () =>
      applyCacheChanges(queryClient, [{ kind: "app", projectId }]),
  });
}

export function useUpdateApp(projectId: string, appId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["UpdateAppRequest"]) =>
      unwrap(
        await api.PATCH("/v1/projects/{projectID}/apps/{appID}", {
          params: { path: { projectID: projectId, appID: appId } },
          body,
        }),
      ),
    onSuccess: () =>
      applyCacheChanges(queryClient, [{ kind: "app", projectId, appId }]),
  });
}

export function useDeleteApp(projectId: string, appId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () =>
      unwrap(
        await api.DELETE("/v1/projects/{projectID}/apps/{appID}", {
          params: { path: { projectID: projectId, appID: appId } },
        }),
      ),
    onSuccess: () =>
      applyCacheChanges(queryClient, [{ kind: "app", projectId, appId }]),
  });
}

export function useCreateAppDeployment(projectId: string, appId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (form: FormData) =>
      uploadMultipart(
        (body, signal) =>
          api.POST("/v1/projects/{projectID}/apps/{appID}/deployments", {
            params: { path: { projectID: projectId, appID: appId } },
            // The browser sends multipart file parts; generated OpenAPI
            // models binary parts as string for the schema-only client.
            body: body as unknown as components["schemas"]["AppDeploymentUploadRequest"],
            signal,
          }),
        form,
      ),
    onSuccess: (result) =>
      applyCacheChanges(queryClient, [
        {
          kind: "app-deployment",
          projectId,
          appId,
          deploymentId: result?.deployment.id,
        },
      ]),
  });
}

export function useSelectAppDeployment(projectId: string, appId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (deploymentId: string) =>
      unwrap(
        await api.POST(
          "/v1/projects/{projectID}/apps/{appID}/deployments/{deploymentID}/select",
          {
            params: {
              path: {
                projectID: projectId,
                appID: appId,
                deploymentID: deploymentId,
              },
            },
          },
        ),
      ),
    onSuccess: (_result, deploymentId) =>
      applyCacheChanges(queryClient, [
        { kind: "app-deployment", projectId, appId, deploymentId },
      ]),
  });
}

export function useRollbackAppDeployment(projectId: string, appId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (deploymentId: string) =>
      unwrap(
        await api.POST(
          "/v1/projects/{projectID}/apps/{appID}/deployments/{deploymentID}/rollback",
          {
            params: {
              path: {
                projectID: projectId,
                appID: appId,
                deploymentID: deploymentId,
              },
            },
          },
        ),
      ),
    onSuccess: (_result, deploymentId) =>
      applyCacheChanges(queryClient, [
        { kind: "app-deployment", projectId, appId, deploymentId },
      ]),
  });
}

export function useCreateAppEnvironmentVariable(
  projectId: string,
  appId: string,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["CreateAppEnvironmentVariableRequest"],
    ) =>
      unwrap(
        await api.POST("/v1/projects/{projectID}/apps/{appID}/variables", {
          params: { path: { projectID: projectId, appID: appId } },
          body,
        }),
      ),
    onSuccess: () =>
      applyCacheChanges(queryClient, [
        { kind: "app-environment-variable", projectId, appId },
      ]),
  });
}

export function useUpdateAppEnvironmentVariable(
  projectId: string,
  appId: string,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({
      variableId,
      body,
    }: {
      variableId: string;
      body: components["schemas"]["UpdateAppEnvironmentVariableRequest"];
    }) =>
      unwrap(
        await api.PATCH(
          "/v1/projects/{projectID}/apps/{appID}/variables/{variableID}",
          {
            params: {
              path: {
                projectID: projectId,
                appID: appId,
                variableID: variableId,
              },
            },
            body,
          },
        ),
      ),
    onSuccess: () =>
      applyCacheChanges(queryClient, [
        { kind: "app-environment-variable", projectId, appId },
      ]),
  });
}

export function useDeleteAppEnvironmentVariable(
  projectId: string,
  appId: string,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (variableId: string) =>
      unwrap(
        await api.DELETE(
          "/v1/projects/{projectID}/apps/{appID}/variables/{variableID}",
          {
            params: {
              path: {
                projectID: projectId,
                appID: appId,
                variableID: variableId,
              },
            },
          },
        ),
      ),
    onSuccess: () =>
      applyCacheChanges(queryClient, [
        { kind: "app-environment-variable", projectId, appId },
      ]),
  });
}

export function useDeleteAppDeployment(projectId: string, appId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (deploymentId: string) =>
      unwrap(
        await api.DELETE(
          "/v1/projects/{projectID}/apps/{appID}/deployments/{deploymentID}",
          {
            params: {
              path: {
                projectID: projectId,
                appID: appId,
                deploymentID: deploymentId,
              },
            },
          },
        ),
      ),
    onSuccess: (_result, deploymentId) =>
      applyCacheChanges(queryClient, [
        { kind: "app-deployment", projectId, appId, deploymentId },
      ]),
  });
}
