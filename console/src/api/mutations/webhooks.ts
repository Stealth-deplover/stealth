"use client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { queryKeys } from "@/api/query-keys";
import type { components } from "@/api/generated/schema";

export function useCreateWebhook(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["CreateWebhookRequest"]) =>
      unwrap(
        await api.POST("/v1/projects/{projectID}/webhooks", {
          params: { path: { projectID: projectId } },
          body,
        }),
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.webhooks(projectId),
      }),
  });
}

export function useRotateWebhookSecret(projectId: string, webhookId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () =>
      unwrap(
        await api.POST(
          "/v1/projects/{projectID}/webhooks/{webhookID}/rotate-secret",
          { params: { path: { projectID: projectId, webhookID: webhookId } } },
        ),
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.webhooks(projectId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.webhook(projectId, webhookId),
      });
    },
  });
}

export function useUpdateWebhook(projectId: string, webhookId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["UpdateWebhookRequest"]) =>
      unwrap(
        await api.PATCH("/v1/projects/{projectID}/webhooks/{webhookID}", {
          params: { path: { projectID: projectId, webhookID: webhookId } },
          body,
        }),
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.webhooks(projectId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.webhook(projectId, webhookId),
      });
    },
  });
}

export function useDeleteWebhook(projectId: string, webhookId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () =>
      unwrap(
        await api.DELETE("/v1/projects/{projectID}/webhooks/{webhookID}", {
          params: { path: { projectID: projectId, webhookID: webhookId } },
        }),
      ),
    onSuccess: () => {
      queryClient.removeQueries({
        queryKey: queryKeys.webhook(projectId, webhookId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.webhooks(projectId),
      });
    },
  });
}
