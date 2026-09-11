"use client";

import { useQuery } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { queryKeys } from "@/api/query-keys";

export function useBootstrapStatus() {
  return useQuery({
    queryKey: queryKeys.bootstrapStatus,
    queryFn: async () => unwrap(await api.GET("/v1/bootstrap/status")),
    retry: false,
    staleTime: 15_000,
  });
}
