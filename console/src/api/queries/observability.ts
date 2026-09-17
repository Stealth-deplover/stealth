"use client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, cancellableQuery } from "@/api/client";
import { type CursorQuery, withCursorPage } from "@/api/pagination";
import { queryKeys } from "@/api/query-keys";

export type AdminTelemetryQuery = {
  from?: string;
  to?: string;
  service?: string;
  level?: string;
  query?: string;
  trace_id?: string;
  min_duration_ms?: number;
  name?: string;
  limit?: number;
};

export function useAdminOverview(options?: {
  refetchInterval?: number | false;
}) {
  return useQuery({
    queryKey: queryKeys.adminOverview,
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/overview", { signal }),
    ),
    refetchInterval: options?.refetchInterval ?? 30_000,
  });
}

export function useAdminLogs(
  query: AdminTelemetryQuery = {},
  options?: { refetchInterval?: number | false },
) {
  return useQuery({
    queryKey: queryKeys.adminTelemetry("logs", query),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/telemetry/logs", {
        params: { query },
        signal,
      }),
    ),
    placeholderData: keepPreviousData,
    refetchInterval: options?.refetchInterval,
  });
}

export function useAdminTraces(
  query: AdminTelemetryQuery = {},
  options?: { refetchInterval?: number | false },
) {
  return useQuery({
    queryKey: queryKeys.adminTelemetry("traces", query),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/telemetry/traces", {
        params: { query },
        signal,
      }),
    ),
    placeholderData: keepPreviousData,
    refetchInterval: options?.refetchInterval,
  });
}

export function useAdminMetrics(
  query: AdminTelemetryQuery = {},
  options?: { refetchInterval?: number | false },
) {
  return useQuery({
    queryKey: queryKeys.adminTelemetry("metrics", query),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/telemetry/metrics", {
        params: { query },
        signal,
      }),
    ),
    placeholderData: keepPreviousData,
    refetchInterval: options?.refetchInterval,
  });
}

export function useAdminSources(
  query: Pick<AdminTelemetryQuery, "from" | "to" | "limit"> = {},
  options?: { refetchInterval?: number | false },
) {
  return useQuery({
    queryKey: queryKeys.adminTelemetry("sources", query),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/telemetry/sources", {
        params: { query },
        signal,
      }),
    ),
    placeholderData: keepPreviousData,
    refetchInterval: options?.refetchInterval,
  });
}

export function useOrganizationAudit(
  organizationId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [
      ...queryKeys.audit("organization", organizationId ?? ""),
      params,
    ],
    enabled: Boolean(organizationId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/organizations/{organizationID}/audit-events", {
        params: { path: { organizationID: organizationId! }, query: params },
        signal,
      }),
    ),
    placeholderData: keepPreviousData,
  });
}

export function useOrganizationTraces(
  organizationId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [
      ...queryKeys.traces("organization", organizationId ?? ""),
      params,
    ],
    enabled: Boolean(organizationId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/organizations/{organizationID}/traces", {
        params: { path: { organizationID: organizationId! }, query: params },
        signal,
      }),
    ),
    placeholderData: keepPreviousData,
  });
}

export function useProjectAudit(
  projectId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.audit("project", projectId ?? ""), params],
    enabled: Boolean(projectId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/audit-events", {
        params: { path: { projectID: projectId! }, query: params },
        signal,
      }),
    ),
    placeholderData: keepPreviousData,
  });
}

export function useProjectTraces(
  projectId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.traces("project", projectId ?? ""), params],
    enabled: Boolean(projectId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/traces", {
        params: { path: { projectID: projectId! }, query: params },
        signal,
      }),
    ),
    placeholderData: keepPreviousData,
  });
}
