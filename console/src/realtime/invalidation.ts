import {
  invalidationKeysFor,
  type CacheChange,
  type CacheQueryKey,
} from "@/api/cache-coherence";

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

  if (type.startsWith("webhook.")) {
    return [
      {
        kind: "webhook",
        projectId,
        webhookId: stringValue(data.webhook_id),
        delivery: type.startsWith("webhook.delivery."),
      },
    ];
  }

  if (type.startsWith("messaging.")) {
    return [{ kind: "messaging", projectId }];
  }

  if (type.startsWith("database.") || type.startsWith("database_")) {
    return [{ kind: "database", projectId }];
  }

  if (type.startsWith("storage_")) {
    return [{ kind: "storage-bucket", projectId }];
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
  return [];
}

export function realtimeInvalidationKeys(
  projectId: string,
  event: RealtimeNotification,
): CacheQueryKey[] {
  return realtimeCacheChanges(projectId, event).flatMap(invalidationKeysFor);
}

export function eventTypesForProjectStream() {
  return [
    "agent.run.accepted",
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
    "function_deployment.create",
    "function_deployment.activate",
    "function_deployment.updated",
    "function_deployment.delete",
    "site_deployment.create",
    "site_deployment.activate",
    "site_deployment.updated",
    "site_deployment.delete",
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
    "project.update",
    "function.create",
    "function.update",
    "function.delete",
    "site.create",
    "site.update",
    "site.delete",
    "storage_bucket.create",
    "storage_bucket.update",
    "storage_bucket.delete",
    "database.create",
    "database.delete",
  ] as const;
}
