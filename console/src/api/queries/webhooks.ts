"use client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { type CursorQuery, withCursorPage } from "@/api/pagination";
import { queryKeys } from "@/api/query-keys";

export function useWebhooks(
  projectId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.webhooks(projectId ?? ""), params],
    enabled: Boolean(projectId),
    queryFn: async () =>
      unwrap(
        await api.GET("/v1/projects/{projectID}/webhooks", {
          params: { path: { projectID: projectId! }, query: params },
        }),
      ),
    placeholderData: keepPreviousData,
  });
}

export function useWebhook(
  projectId: string | undefined,
  webhookId: string | undefined,
) {
  return useQuery({
    queryKey: queryKeys.webhook(projectId ?? "", webhookId ?? ""),
    enabled: Boolean(projectId && webhookId),
    queryFn: async () =>
      unwrap(
        await api.GET("/v1/projects/{projectID}/webhooks/{webhookID}", {
          params: { path: { projectID: projectId!, webhookID: webhookId! } },
        }),
      ),
  });
}

export function useWebhookDeliveries(
  projectId: string | undefined,
  webhookId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [
      ...queryKeys.webhookDeliveries(projectId ?? "", webhookId ?? ""),
      params,
    ],
    enabled: Boolean(projectId && webhookId),
    queryFn: async () =>
      unwrap(
        await api.GET(
          "/v1/projects/{projectID}/webhooks/{webhookID}/deliveries",
          {
            params: {
              path: { projectID: projectId!, webhookID: webhookId! },
              query: params,
            },
          },
        ),
      ),
    placeholderData: keepPreviousData,
    refetchInterval: (query) => {
      const deliveries = query.state.data?.deliveries ?? [];
      return deliveries.some(
        (delivery) =>
          delivery.status === "pending" || delivery.status === "running",
      )
        ? 5000
        : false;
    },
  });
}
