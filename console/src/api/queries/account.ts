"use client";
import { useQuery } from "@tanstack/react-query";
import { api, cancellableQuery } from "@/api/client";
import { queryKeys } from "@/api/query-keys";

export function useCurrentAccount() {
  return useQuery({
    queryKey: queryKeys.account,
    queryFn: cancellableQuery((signal) => api.GET("/v1/account", { signal })),
    retry: false,
    staleTime: 60_000,
  });
}

export function useAccountSessions() {
  return useQuery({
    queryKey: queryKeys.accountSessions,
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/account/sessions", { signal }),
    ),
    retry: false,
  });
}
