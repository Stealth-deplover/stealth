"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { queryKeys } from "@/api/query-keys";
import type { components } from "@/api/generated/schema";

export function useCreateAdminMonitor() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["CreateAdminMonitorRequest"],
    ) => unwrap(await api.POST("/v1/admin/monitors", { body })),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: queryKeys.adminMonitors }),
  });
}

export function useUpdateAdminMonitor(monitorId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["UpdateAdminMonitorRequest"],
    ) =>
      unwrap(
        await api.PUT("/v1/admin/monitors/{monitorID}", {
          params: { path: { monitorID: monitorId } },
          body,
        }),
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.adminMonitors,
      });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.adminMonitor(monitorId),
      });
    },
  });
}

export function useDeleteAdminMonitor() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (monitorId: string) =>
      unwrap(
        await api.DELETE("/v1/admin/monitors/{monitorID}", {
          params: { path: { monitorID: monitorId } },
        }),
      ),
    onSuccess: (_, monitorId) => {
      queryClient.removeQueries({
        queryKey: queryKeys.adminMonitor(monitorId),
      });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.adminMonitors,
      });
    },
  });
}

export function useCreateAdminAlert() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["CreateAdminAlertRuleRequest"],
    ) => unwrap(await api.POST("/v1/admin/alerts", { body })),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: queryKeys.adminAlerts }),
  });
}

export function useDeleteAdminAlert() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (alertRuleId: string) =>
      unwrap(
        await api.DELETE("/v1/admin/alerts/{alertRuleID}", {
          params: { path: { alertRuleID: alertRuleId } },
        }),
      ),
    onSuccess: (_, alertRuleId) => {
      queryClient.removeQueries({
        queryKey: queryKeys.adminAlert(alertRuleId),
      });
      void queryClient.invalidateQueries({ queryKey: queryKeys.adminAlerts });
    },
  });
}

export function useUpdateAdminErrorStatus() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({
      fingerprint,
      status,
    }: {
      fingerprint: string;
      status: components["schemas"]["UpdateAdminErrorGroupStatusRequest"]["status"];
    }) =>
      unwrap(
        await api.PATCH("/v1/admin/telemetry/errors/{fingerprint}", {
          params: { path: { fingerprint } },
          body: { status },
        }),
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: ["admin", "telemetry", "errors"],
      }),
  });
}

export function useCreateAdminNotificationChannel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["CreateAdminNotificationChannelRequest"],
    ) => unwrap(await api.POST("/v1/admin/notifications", { body })),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: queryKeys.adminNotifications }),
  });
}

export function useDeleteAdminNotificationChannel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (channelId: string) =>
      unwrap(
        await api.DELETE("/v1/admin/notifications/{channelID}", {
          params: { path: { channelID: channelId } },
        }),
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: queryKeys.adminNotifications }),
  });
}

export function useCreateAdminIncident() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["CreateAdminIncidentRequest"],
    ) => unwrap(await api.POST("/v1/admin/incidents", { body })),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: queryKeys.adminIncidents }),
  });
}

export function useUpdateAdminIncident(incidentId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["UpdateAdminIncidentRequest"],
    ) =>
      unwrap(
        await api.PATCH("/v1/admin/incidents/{incidentID}", {
          params: { path: { incidentID: incidentId } },
          body,
        }),
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.adminIncidents,
      });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.adminIncident(incidentId),
      });
    },
  });
}

export function useAddAdminIncidentEvent(incidentId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["AddAdminIncidentEventRequest"],
    ) =>
      unwrap(
        await api.POST("/v1/admin/incidents/{incidentID}/events", {
          params: { path: { incidentID: incidentId } },
          body,
        }),
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.adminIncident(incidentId),
      }),
  });
}

export function useCreateAdminDashboard() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["CreateAdminDashboardRequest"],
    ) => unwrap(await api.POST("/v1/admin/dashboards", { body })),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: queryKeys.adminDashboards }),
  });
}

export function useUpdateAdminDashboard(dashboardId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["CreateAdminDashboardRequest"],
    ) =>
      unwrap(
        await api.PUT("/v1/admin/dashboards/{dashboardID}", {
          params: { path: { dashboardID: dashboardId } },
          body,
        }),
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.adminDashboards,
      });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.adminDashboard(dashboardId),
      });
    },
  });
}

export function useDeleteAdminDashboard() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (dashboardId: string) =>
      unwrap(
        await api.DELETE("/v1/admin/dashboards/{dashboardID}", {
          params: { path: { dashboardID: dashboardId } },
        }),
      ),
    onSuccess: (_, dashboardId) => {
      queryClient.removeQueries({
        queryKey: queryKeys.adminDashboard(dashboardId),
      });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.adminDashboards,
      });
    },
  });
}

export function useUpdateAdminStatusPage() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["UpdateAdminStatusPageRequest"],
    ) => unwrap(await api.PUT("/v1/admin/status-page", { body })),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: queryKeys.adminStatusPage }),
  });
}
