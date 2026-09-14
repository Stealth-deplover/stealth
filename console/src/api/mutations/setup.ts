"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { queryKeys } from "@/api/query-keys";
import type { components } from "@/api/generated/schema";

const setupMutation = {
  params: { header: { "X-Stealth-Setup": "1" as const } },
};

function cacheSetupState(
  queryClient: ReturnType<typeof useQueryClient>,
  state: components["schemas"]["SetupState"] | undefined,
) {
  if (!state) return;
  queryClient.setQueryData<components["schemas"]["SetupStatusResponse"]>(
    queryKeys.setupStatus,
    (current) => (current ? { ...current, state } : current),
  );
}

export function useSaveSetupConfig() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["SetupConfigRequest"]) =>
      unwrap(
        await api.PUT("/v1/setup/config", {
          ...setupMutation,
          body,
        }),
      ),
    onSuccess: (state) => cacheSetupState(queryClient, state),
  });
}

export function useStartSetupGitHubManifest() {
  return useMutation({
    mutationFn: async () =>
      unwrap(await api.POST("/v1/setup/github/manifest/start", setupMutation)),
  });
}

export function useStartSetupGitHubAuthorization() {
  return useMutation({
    mutationFn: async () =>
      unwrap(await api.POST("/v1/setup/github/authorize/start", setupMutation)),
  });
}

export function useSaveSetupGitHubManual() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["SetupGitHubManualRequest"],
    ) =>
      unwrap(
        await api.POST("/v1/setup/github/manual", {
          ...setupMutation,
          body,
        }),
      ),
    onSuccess: (state) => cacheSetupState(queryClient, state),
  });
}

export function useSaveSetupCloudflareToken() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["SetupCloudflareTokenRequest"],
    ) =>
      unwrap(
        await api.POST("/v1/setup/cloudflare/token", {
          ...setupMutation,
          body,
        }),
      ),
    onSuccess: (state) => cacheSetupState(queryClient, state),
  });
}

export function useCreateSetupCloudflareTunnel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["SetupCloudflareTunnelRequest"],
    ) =>
      unwrap(
        await api.POST("/v1/setup/cloudflare/tunnel", {
          ...setupMutation,
          body,
        }),
      ),
    onSuccess: (state) => cacheSetupState(queryClient, state),
  });
}

export function useTestSetupDatabase() {
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["SetupDatabaseTestRequest"],
    ) =>
      unwrap(
        await api.POST("/v1/setup/infrastructure/database/test", {
          ...setupMutation,
          body,
        }),
      ),
  });
}

export function useTestSetupRedis() {
  return useMutation({
    mutationFn: async (body: components["schemas"]["SetupRedisTestRequest"]) =>
      unwrap(
        await api.POST("/v1/setup/infrastructure/redis/test", {
          ...setupMutation,
          body,
        }),
      ),
  });
}

export function useTestSetupStorage() {
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["SetupStorageTestRequest"],
    ) =>
      unwrap(
        await api.POST("/v1/setup/infrastructure/storage/test", {
          ...setupMutation,
          body,
        }),
      ),
  });
}

export function useStartSetupInstall() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () =>
      unwrap(
        await api.POST("/v1/setup/install", {
          ...setupMutation,
          body: {},
        }),
      ),
    onSuccess: (result) => {
      cacheSetupState(queryClient, result?.state);
    },
  });
}

export function useIssueSetupHandoffToken() {
  return useMutation({
    mutationFn: async () => unwrap(await api.GET("/v1/setup/handoff-token")),
  });
}
