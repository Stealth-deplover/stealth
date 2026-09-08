"use client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap, uploadMultipart } from "@/api/client";
import { queryKeys } from "@/api/query-keys";
import type { components } from "@/api/generated/schema";

export function useCreateSite(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["CreateSiteRequest"]) =>
      unwrap(
        await api.POST("/v1/projects/{projectID}/sites", {
          params: { path: { projectID: projectId } },
          body,
        }),
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: queryKeys.sites(projectId) }),
  });
}

export function useUploadSiteDeployment(projectId: string, siteId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({
      file,
      activate = true,
    }: {
      file: File;
      activate?: boolean;
    }) => {
      const form = new FormData();
      form.append("source", file);
      form.append("activate", activate ? "true" : "false");
      return uploadMultipart<components["schemas"]["SiteDeploymentResponse"]>(
        `/v1/projects/${projectId}/sites/${siteId}/deployments`,
        form,
      );
    },
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.site(projectId, siteId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.siteDeployments(projectId, siteId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.siteDeploymentScope(projectId, siteId),
      });
    },
  });
}

export function useActivateSiteDeployment(projectId: string, siteId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (deploymentId: string) =>
      unwrap(
        await api.POST(
          "/v1/projects/{projectID}/sites/{siteID}/deployments/{deploymentID}/activate",
          {
            params: {
              path: {
                projectID: projectId,
                siteID: siteId,
                deploymentID: deploymentId,
              },
            },
          },
        ),
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.site(projectId, siteId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.siteDeployments(projectId, siteId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.siteDeploymentScope(projectId, siteId),
      });
    },
  });
}
