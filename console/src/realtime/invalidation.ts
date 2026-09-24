import {
  invalidationKeysFor,
  type CacheChange,
  type CacheQueryKey,
} from "@/api/cache-coherence";
import { queryKeys } from "@/api/query-keys";

export type RealtimeNotification = {
  type?: string;
  event?: string;
  project_id?: string;
  resource_id?: string;
  target?: { id?: string | null };
  payload?: Record<string, unknown>;
  data?: Record<string, unknown>;
};

function stringValue(value: unknown) {
  return typeof value === "string" && value.length > 0 ? value : undefined;
}

function metadata(event: RealtimeNotification) {
  return event.payload ?? event.data ?? {};
}

function resourceId(event: RealtimeNotification) {
  return stringValue(event.resource_id) ?? stringValue(event.target?.id);
}

const projectRealtimeEventTypes = [
  "agent.run.accepted",
  "agent.run.queued",
  "agent.run.running",
  "agent.run.completed",
  "agent.run.failed",
  "agent.run.cancelled",
  "agent.create",
  "agent.update",
  "agent.delete",
  "function_execution.accept",
  "function_execution.running",
  "function_execution.succeeded",
  "function_execution.failed",
  "function_execution.cancelled",
  "function_variable.create",
  "function_variable.update",
  "function_variable.delete",
  "function_deployment.create",
  "function_deployment.activate",
  "function_deployment.updated",
  "function_deployment.delete",
  "site_deployment.create",
  "site_deployment.activate",
  "site_deployment.updated",
  "site_deployment.delete",
  "app_deployment.create",
  "app_deployment.select",
  "app_deployment.delete",
  "app_deployment.updated",
  "app.runtime.updated",
  "site_domain.create",
  "site_domain.delete",
  "site_domain.verify",
  "webhook.create",
  "webhook.update",
  "webhook.delete",
  "webhook.delivery.updated",
  "messaging.provider.create",
  "messaging.provider.update",
  "messaging.provider.delete",
  "messaging.topic.create",
  "messaging.topic.update",
  "messaging.topic.delete",
  "messaging.delivery.updated",
  "messaging.subscriber.create",
  "messaging.subscriber.delete",
  "messaging.message.create",
  "messaging.message.cancel",
  "webhook.secret_rotate",
  "project.update",
  "project_auth.settings_update",
  "project_api_key.create",
  "project_api_key.revoke",
  "project_user.create",
  "project_user.delete",
  "project_user.status_change",
  "project_user.email_verify",
  "project_user.password_reset",
  "function.create",
  "function.update",
  "function.delete",
  "site.create",
  "site.update",
  "site.delete",
  "app.create",
  "app.update",
  "app.delete",
  "storage_bucket.create",
  "storage_bucket.update",
  "storage_bucket.delete",
  "database.create",
  "database.delete",
  "database_backup.create",
  "database_backup.delete",
  "database_backup.restore",
  "database_table.create",
  "database_table.update",
  "database_table.delete",
  "database_column.create",
  "database_column.delete",
  "database_index.create",
  "database_index.delete",
  "database_relationship.create",
  "database_relationship.delete",
  "database_row.create",
  "database_row.update",
  "database_row.delete",
  "storage_file.create",
  "storage_file.update",
  "storage_file.delete",
] as const;

const projectRealtimeEventTypeSet = new Set<string>(projectRealtimeEventTypes);

/**
 * Converts a wire notification into a semantic cache change. The event is
 * never used as a state snapshot: callers invalidate these keys and refetch
 * the Go API source of truth.
 */
export function realtimeCacheChanges(
  projectId: string,
  event: RealtimeNotification,
): CacheChange[] {
  const type = stringValue(event.type) ?? stringValue(event.event) ?? "";
  if (!projectRealtimeEventTypeSet.has(type)) return [];
  const data = metadata(event);

  if (type.startsWith("agent.run.")) {
    const agentId = stringValue(data.agent_id);
    const runId = resourceId(event);
    return [
      {
        kind: "agent-run",
        projectId,
        agentId,
        runId,
        includeAgent: true,
        includeAgentDetail: true,
      },
    ];
  }

  if (type.startsWith("agent.")) {
    return [{ kind: "agent", projectId, agentId: resourceId(event) }];
  }

  if (type.startsWith("function_execution.")) {
    return [
      {
        kind: "function-execution",
        projectId,
        functionId: stringValue(data.function_id),
        executionId: resourceId(event),
      },
    ];
  }

  if (type.startsWith("function_variable.")) {
    const functionId = stringValue(data.function_id);
    if (!functionId) return [];
    return [{ kind: "function-variable", projectId, functionId }];
  }

  if (type.startsWith("function_deployment.")) {
    const functionId = stringValue(data.function_id);
    if (!functionId) return [];
    return [
      {
        kind: "function-deployment",
        projectId,
        functionId,
        deploymentId: resourceId(event),
      },
    ];
  }

  if (type.startsWith("site_deployment.")) {
    const siteId = stringValue(data.site_id);
    if (!siteId) return [];
    return [
      {
        kind: "site-deployment",
        projectId,
        siteId,
        deploymentId: resourceId(event),
      },
    ];
  }

  if (type.startsWith("app_deployment.")) {
    const appId = stringValue(data.app_id);
    if (!appId) return [];
    return [
      {
        kind: "app-deployment",
        projectId,
        appId,
        deploymentId: resourceId(event),
      },
      { kind: "app", projectId, appId },
    ];
  }

  if (type.startsWith("webhook.")) {
    const webhookId = stringValue(data.webhook_id) ?? resourceId(event);
    return [
      {
        kind: "webhook",
        projectId,
        webhookId,
        includeDetail: true,
        delivery: type.startsWith("webhook.delivery."),
      },
    ];
  }

  if (type.startsWith("messaging.")) {
    return [{ kind: "messaging", projectId }];
  }

  if (type === "project_auth.settings_update") {
    return [{ kind: "auth-settings", projectId }];
  }

  if (type.startsWith("database_backup.")) {
    const databaseId =
      stringValue(data.database_id) ??
      (type === "database_backup.restore" ? resourceId(event) : undefined);
    if (!databaseId) return [];
    return [
      {
        kind: "database-backup",
        projectId,
        databaseId,
        restore: type === "database_backup.restore",
      },
    ];
  }

  if (type.startsWith("database_table.")) {
    const databaseId = stringValue(data.database_id);
    if (!databaseId) return [];
    return [
      {
        kind: "database-table",
        projectId,
        databaseId,
        tableId: resourceId(event),
      },
    ];
  }

  if (
    type.startsWith("database_column.") ||
    type.startsWith("database_index.")
  ) {
    const databaseId = stringValue(data.database_id);
    const tableId = stringValue(data.table_id);
    if (!databaseId || !tableId) return [];
    return [{ kind: "database-table-schema", projectId, databaseId, tableId }];
  }

  if (type.startsWith("database_relationship.")) {
    const databaseId = stringValue(data.database_id);
    const tableIds = [
      stringValue(data.source_table_id),
      stringValue(data.target_table_id),
    ].filter((value): value is string => Boolean(value));
    if (!databaseId || tableIds.length === 0) return [];
    return tableIds.map((tableId) => ({
      kind: "database-table-schema" as const,
      projectId,
      databaseId,
      tableId,
    }));
  }

  if (type.startsWith("database_row.")) {
    const databaseId = stringValue(data.database_id);
    const tableId = stringValue(data.table_id);
    if (!databaseId || !tableId) return [];
    return [
      {
        kind: "database-table-rows",
        projectId,
        databaseId,
        tableId,
      },
    ];
  }

  if (type.startsWith("database.") || type.startsWith("database_")) {
    return [{ kind: "database", projectId }];
  }

  if (type.startsWith("storage_file.")) {
    const bucketId = stringValue(data.bucket_id);
    if (!bucketId) return [];
    const operation =
      type === "storage_file.update"
        ? "rename"
        : type === "storage_file.create"
          ? "upload"
          : type === "storage_file.delete"
            ? "delete"
            : undefined;
    if (!operation) return [];
    return [
      {
        kind: "storage-file",
        projectId,
        bucketId,
        fileId: resourceId(event),
        operation,
      },
    ];
  }

  if (type.startsWith("storage_bucket.")) {
    return [
      {
        kind: "storage-bucket",
        projectId,
        bucketId: resourceId(event),
        includeDetail: true,
      },
    ];
  }

  if (type.startsWith("project_api_key.")) {
    return [
      {
        kind: "api-key",
        projectId,
        keyId: resourceId(event),
      },
    ];
  }

  if (type.startsWith("project_user.")) {
    return [
      {
        kind: "project-user",
        projectId,
        userId: resourceId(event),
      },
    ];
  }

  if (type.startsWith("site_domain.")) {
    const siteId = stringValue(data.site_id);
    if (!siteId) return [];
    return [{ kind: "site", projectId, siteId }];
  }

  if (type.startsWith("project.")) {
    return [{ kind: "project", projectId }];
  }

  if (type.startsWith("function.")) {
    return [{ kind: "function", projectId, functionId: resourceId(event) }];
  }

  if (type.startsWith("site.")) {
    return [{ kind: "site", projectId, siteId: resourceId(event) }];
  }
  if (type.startsWith("app.")) {
    return [{ kind: "app", projectId, appId: resourceId(event) }];
  }
  return [];
}

export function realtimeInvalidationKeys(
  projectId: string,
  event: RealtimeNotification,
): CacheQueryKey[] {
  return realtimeCacheChanges(projectId, event).flatMap(invalidationKeysFor);
}

export function adminRealtimeInvalidationKeys(
  event: RealtimeNotification,
): CacheQueryKey[] {
  const type = stringValue(event.type) ?? stringValue(event.event) ?? "";
  const resource = resourceId(event);
  const keys: CacheQueryKey[] = [["admin", "audit-events"]];
  const add = (key: CacheQueryKey) => {
    if (
      !keys.some(
        (candidate) => JSON.stringify(candidate) === JSON.stringify(key),
      )
    ) {
      keys.push(key);
    }
  };

  if (type.startsWith("admin.monitor.")) {
    add(queryKeys.adminMonitors);
    if (resource) add(queryKeys.adminMonitor(resource));
  } else if (type.startsWith("admin.alert.")) {
    add(queryKeys.adminAlerts);
    add(queryKeys.adminAlertEvents);
    if (resource) add(queryKeys.adminAlert(resource));
  } else if (
    type.startsWith("admin.notification_channel.") ||
    type.startsWith("admin.notification.delivery.")
  ) {
    add(queryKeys.adminNotifications);
  } else if (type.startsWith("admin.incident.")) {
    add(queryKeys.adminIncidents);
    if (resource) add(queryKeys.adminIncident(resource));
  } else if (type.startsWith("admin.dashboard.")) {
    add(queryKeys.adminDashboards);
    if (resource) add(queryKeys.adminDashboard(resource));
  } else if (type.startsWith("admin.status_page.")) {
    add(queryKeys.adminStatusPage);
  } else if (type.startsWith("admin.error_group.")) {
    add(["admin", "telemetry", "errors"]);
  } else {
    // Unknown Admin events still invalidate the bounded control-room overview
    // without forcing every telemetry query to refetch.
    add(queryKeys.adminOverview);
  }
  return keys;
}

export function eventTypesForProjectStream() {
  return projectRealtimeEventTypes;
}
