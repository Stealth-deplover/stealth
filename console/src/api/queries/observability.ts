"use client";
import {
  keepPreviousData,
  useInfiniteQuery,
  useQuery,
} from "@tanstack/react-query";
import { api, apiUrl, cancellableQuery, unwrap } from "@/api/client";
import type { PathsV1AdminInfrastructureMetricsGetParametersQueryScope } from "@/api/generated/schema";
import { type CursorQuery, withCursorPage } from "@/api/pagination";
import { queryKeys } from "@/api/query-keys";
import { useEffect, useMemo, useState } from "react";

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

export type AdminTailLog = {
  timestamp: string;
  trace_id?: string;
  span_id?: string;
  level?: string;
  service: string;
  message: string;
  attributes?: Record<string, string>;
  resource_attributes?: Record<string, string>;
};

export function useAdminLogTail(
  query: Pick<
    AdminTelemetryQuery,
    "from" | "to" | "service" | "level" | "query"
  > & { limit?: number },
  enabled: boolean,
) {
  const params = useMemo(() => {
    const search = new URLSearchParams();
    for (const [key, value] of Object.entries(query)) {
      if (value !== undefined && value !== "") search.set(key, String(value));
    }
    return search.toString();
  }, [query]);
  const streamKey = useMemo(() => ({ enabled, params }), [enabled, params]);
  const [stream, setStream] = useState<{
    key: typeof streamKey | null;
    items: AdminTailLog[];
    error: string | null;
    connected: boolean;
  }>({ key: null, items: [], error: null, connected: false });

  useEffect(() => {
    if (!enabled || typeof EventSource === "undefined") {
      return;
    }
    // A query/filter change starts a fresh bounded stream. Reconnects on the
    // same EventSource retain the current items while the browser resumes
    // from the last SSE event id.
    const source = new EventSource(
      apiUrl(`/v1/admin/telemetry/logs/tail?${params}`),
      { withCredentials: true },
    );
    const onLog = (event: Event) => {
      try {
        const item = JSON.parse((event as MessageEvent).data) as AdminTailLog;
        setStream((current) => {
          const base =
            current.key === streamKey
              ? current
              : {
                  key: streamKey,
                  items: [],
                  error: null,
                  connected: false,
                };
          const seen = new Set<string>();
          const next = [item, ...base.items].filter((candidate) => {
            const candidateKey = `${candidate.timestamp}\u0000${candidate.trace_id ?? ""}\u0000${candidate.span_id ?? ""}\u0000${candidate.service}\u0000${candidate.message}`;
            if (seen.has(candidateKey)) return false;
            seen.add(candidateKey);
            return true;
          });
          return { ...base, items: next.slice(0, 250), error: null };
        });
      } catch {
        setStream((current) => ({
          ...(current.key === streamKey
            ? current
            : { key: streamKey, items: [], connected: false }),
          error: "The live log stream returned an invalid event.",
        }));
      }
    };
    const onStreamError = (event: Event) => {
      try {
        const payload = JSON.parse((event as MessageEvent).data) as {
          message?: string;
        };
        setStream((current) => ({
          ...(current.key === streamKey
            ? current
            : { key: streamKey, items: [], connected: false }),
          error: payload.message ?? "Live log stream is unavailable.",
        }));
      } catch {
        setStream((current) => ({
          ...(current.key === streamKey
            ? current
            : { key: streamKey, items: [], connected: false }),
          error: "Live log stream is unavailable.",
        }));
      }
    };
    const onConnectionError = () => {
      setStream((current) => ({
        ...(current.key === streamKey
          ? current
          : { key: streamKey, items: [], connected: false }),
        connected: false,
        error: "Live log stream disconnected. The browser will retry.",
      }));
    };
    source.addEventListener("log", onLog);
    source.addEventListener("stream_error", onStreamError);
    source.onerror = onConnectionError;
    source.onopen = () => {
      setStream((current) => ({
        ...(current.key === streamKey
          ? current
          : { key: streamKey, items: [], connected: false }),
        connected: true,
        error: null,
      }));
    };
    return () => {
      source.removeEventListener("log", onLog);
      source.removeEventListener("stream_error", onStreamError);
      source.onerror = null;
      source.close();
    };
  }, [enabled, params, streamKey]);

  const isCurrentStream = stream.key === streamKey && enabled;

  return {
    items: isCurrentStream ? stream.items : [],
    error: isCurrentStream ? stream.error : null,
    connected: isCurrentStream && stream.connected,
  };
}

export function useAdminOverview(options?: {
  query?: Pick<AdminTelemetryQuery, "from" | "to">;
  refetchInterval?: number | false;
}) {
  return useQuery({
    queryKey: [...queryKeys.adminOverview, options?.query ?? {}],
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/overview", {
        params: { query: options?.query ?? {} },
        signal,
      }),
    ),
    refetchInterval: options?.refetchInterval ?? 30_000,
  });
}

export function usePublicStatusPage() {
  return useQuery({
    queryKey: queryKeys.publicStatusPage,
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/status-page", { signal }),
    ),
    retry: false,
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
  options?: { enabled?: boolean; refetchInterval?: number | false },
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
    enabled: options?.enabled ?? true,
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

export function useAdminAlertEvents(
  query: { from?: string; to?: string; limit?: number } = {},
  options?: { refetchInterval?: number | false },
) {
  const pageQuery = { ...query };
  return useInfiniteQuery({
    queryKey: [...queryKeys.adminAlertEvents, pageQuery],
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ signal, pageParam }) => {
      const result = await api.GET("/v1/admin/alert-events", {
        params: { query: { ...pageQuery, cursor: pageParam } },
        signal,
      });
      return unwrap(result);
    },
    getNextPageParam: (lastPage) => lastPage?.next_cursor ?? undefined,
    refetchInterval: options?.refetchInterval,
  });
}

export function useAdminNotificationChannels(
  query: { limit?: number } = {},
  options?: { refetchInterval?: number | false },
) {
  return useQuery({
    queryKey: [...queryKeys.adminNotifications, query],
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/admin/notifications", { params: { query }, signal }),
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
