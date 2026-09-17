"use client";

import { useQuery } from "@tanstack/react-query";
import { api, cancellableQuery } from "@/api/client";
import { queryKeys } from "@/api/query-keys";

export function useBootstrapStatus() {
  return useQuery({
    queryKey: queryKeys.bootstrapStatus,
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/bootstrap/status", { signal }),
    ),
    retry: false,
    staleTime: 15_000,
  });
}
