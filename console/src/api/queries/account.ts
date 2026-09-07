"use client";
import { useQuery } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { queryKeys } from "@/api/query-keys";

export function useCurrentAccount() {
  return useQuery({
    queryKey: queryKeys.account,
    queryFn: async () => unwrap(await api.GET("/v1/account")),
    retry: false,
    staleTime: 60_000,
  });
}

export function useAccountSessions() {
  return useQuery({
    queryKey: queryKeys.accountSessions,
    queryFn: async () => unwrap(await api.GET("/v1/account/sessions")),
    retry: false,
  });
}
