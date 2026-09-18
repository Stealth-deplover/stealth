"use client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, cancellableQuery } from "@/api/client";
import type { PathsV1AdminInfrastructureMetricsGetParametersQueryScope } from "@/api/generated/schema";
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

export function useAdminOperations(
  query: Pick<AdminTelemetryQuery, "from" | "to" | "limit"> = {},
  options?: { refetchInterval?: number | false },
) {
  return useQuery({
    queryKey: [...queryKeys.adminOperations, query],
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/operations", { params: { query }, signal }),
    ),
    placeholderData: keepPreviousData,
    refetchInterval: options?.refetchInterval,
  });
}

export function useAdminAuditEvents(
  query: { before?: string; limit?: number } = {},
  options?: { refetchInterval?: number | false },
) {
  return useQuery({
    queryKey: queryKeys.adminAuditEvents(query),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/audit-events", { params: { query }, signal }),
    ),
    placeholderData: keepPreviousData,
    refetchInterval: options?.refetchInterval,
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

export function useAdminLogVolume(
  query: AdminTelemetryQuery = {},
  options?: { refetchInterval?: number | false },
) {
  return useQuery({
    queryKey: queryKeys.adminTelemetry("log-volume", query),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/telemetry/log-volume", { params: { query }, signal }),
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

export function useAdminErrors(
  query: Pick<
    AdminTelemetryQuery,
    "from" | "to" | "service" | "query" | "limit"
  > = {},
  options?: { refetchInterval?: number | false },
) {
  return useQuery({
    queryKey: queryKeys.adminTelemetry("errors", query),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/telemetry/errors", { params: { query }, signal }),
    ),
    placeholderData: keepPreviousData,
    refetchInterval: options?.refetchInterval,
  });
}

export function useAdminServiceMap(
  query: Pick<AdminTelemetryQuery, "from" | "to" | "limit"> = {},
  options?: { refetchInterval?: number | false },
) {
  return useQuery({
    queryKey: queryKeys.adminTelemetry("services", query),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/telemetry/services", { params: { query }, signal }),
    ),
    placeholderData: keepPreviousData,
    refetchInterval: options?.refetchInterval,
  });
}

export function useAdminInfrastructure(
  query: Pick<AdminTelemetryQuery, "from" | "to" | "limit"> & {
    scope?: PathsV1AdminInfrastructureMetricsGetParametersQueryScope;
  } = {},
  options?: { refetchInterval?: number | false },
) {
  return useQuery({
    queryKey: queryKeys.adminInfrastructure(query),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/infrastructure/metrics", {
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

export function useAdminMonitors(
  query: { limit?: number } = {},
  options?: { refetchInterval?: number | false },
) {
  return useQuery({
    queryKey: [...queryKeys.adminMonitors, query],
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/monitors", { params: { query }, signal }),
    ),
    placeholderData: keepPreviousData,
    refetchInterval: options?.refetchInterval,
  });
}

export function useAdminMonitor(
  monitorId: string | undefined,
  options?: { refetchInterval?: number | false },
) {
  return useQuery({
    queryKey: queryKeys.adminMonitor(monitorId ?? ""),
    enabled: Boolean(monitorId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/monitors/{monitorID}", {
        params: { path: { monitorID: monitorId! } },
        signal,
      }),
    ),
    refetchInterval: options?.refetchInterval,
  });
}

export function useAdminAlerts(
  query: { limit?: number } = {},
  options?: { refetchInterval?: number | false },
) {
  return useQuery({
    queryKey: [...queryKeys.adminAlerts, query],
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/alerts", { params: { query }, signal }),
    ),
    placeholderData: keepPreviousData,
    refetchInterval: options?.refetchInterval,
  });
}

export function useAdminAlert(
  alertRuleId: string | undefined,
  options?: { refetchInterval?: number | false },
) {
  return useQuery({
    queryKey: queryKeys.adminAlert(alertRuleId ?? ""),
    enabled: Boolean(alertRuleId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/alerts/{alertRuleID}", {
        params: { path: { alertRuleID: alertRuleId! } },
        signal,
      }),
    ),
    refetchInterval: options?.refetchInterval,
  });
}

export function useAdminIncidents(
  query: { limit?: number } = {},
  options?: { refetchInterval?: number | false },
) {
  return useQuery({
    queryKey: [...queryKeys.adminIncidents, query],
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/incidents", { params: { query }, signal }),
    ),
    placeholderData: keepPreviousData,
    refetchInterval: options?.refetchInterval,
  });
}

export function useAdminIncident(
  incidentId: string | undefined,
  options?: { refetchInterval?: number | false },
) {
  return useQuery({
    queryKey: queryKeys.adminIncident(incidentId ?? ""),
    enabled: Boolean(incidentId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/incidents/{incidentID}", {
        params: { path: { incidentID: incidentId! } },
        signal,
      }),
    ),
    refetchInterval: options?.refetchInterval,
  });
}

export function useAdminDashboards(
  query: { limit?: number } = {},
  options?: { refetchInterval?: number | false },
) {
  return useQuery({
    queryKey: [...queryKeys.adminDashboards, query],
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/dashboards", { params: { query }, signal }),
    ),
    placeholderData: keepPreviousData,
    refetchInterval: options?.refetchInterval,
  });
}

export function useAdminDashboard(
  dashboardId: string | undefined,
  options?: { refetchInterval?: number | false },
) {
  return useQuery({
    queryKey: queryKeys.adminDashboard(dashboardId ?? ""),
    enabled: Boolean(dashboardId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/dashboards/{dashboardID}", {
        params: { path: { dashboardID: dashboardId! } },
        signal,
      }),
    ),
    refetchInterval: options?.refetchInterval,
  });
}

export function useAdminStatusPage(options?: {
  refetchInterval?: number | false;
}) {
  return useQuery({
    queryKey: queryKeys.adminStatusPage,
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/status-page", { signal }),
    ),
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
