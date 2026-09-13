"use client";

import { useQuery } from "@tanstack/react-query";
import { api, execute } from "@/api/client";
import { queryKeys } from "@/api/query-keys";

export function useSetupStatus() {
  return useQuery({
    queryKey: queryKeys.setupStatus,
    queryFn: ({ signal }) => execute(api.GET("/v1/setup/status", { signal })),
    retry: false,
    staleTime: 2_000,
    refetchInterval: (query) =>
      query.state.data?.state.phase === "installing" ? 2_000 : false,
  });
}

export function useSetupPreflight(enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.setupPreflight,
    queryFn: ({ signal }) =>
      execute(api.GET("/v1/setup/preflight", { signal })),
    enabled,
    retry: false,
    staleTime: 5_000,
  });
}

export function useSetupCloudflareAccounts(enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.setupCloudflareAccounts,
    queryFn: ({ signal }) =>
      execute(api.GET("/v1/setup/cloudflare/accounts", { signal })),
    enabled,
    retry: false,
    staleTime: 30_000,
  });
}

export function useSetupCloudflareZones(accountID: string, enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.setupCloudflareZones(accountID),
    queryFn: ({ signal }) =>
      execute(
        api.GET("/v1/setup/cloudflare/zones", {
          params: { query: { account_id: accountID } },
          signal,
        }),
      ),
    enabled: enabled && Boolean(accountID),
    retry: false,
    staleTime: 30_000,
  });
}
