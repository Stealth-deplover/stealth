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

type QueryKey = readonly unknown[];

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
 * Maps notifications to query prefixes. The event is never used as a state
 * snapshot: callers invalidate these keys and refetch the Go API source of
 * truth.
 */
export function realtimeInvalidationKeys(
  projectId: string,
  event: RealtimeNotification,
): QueryKey[] {
  const type = stringValue(event.type) ?? stringValue(event.event) ?? "";
  const data = metadata(event);
  const keys: QueryKey[] = [];
  const add = (key: QueryKey | undefined) => {
    if (
      key &&
      !keys.some(
        (candidate) => JSON.stringify(candidate) === JSON.stringify(key),
      )
    ) {
      keys.push(key);
    }
  };

  if (type.startsWith("agent.run.")) {
    const agentId = stringValue(data.agent_id);
    const runId = resourceId(event);
    if (agentId) add(queryKeys.agentRuns(agentId));
    if (agentId && runId) add(queryKeys.agentRun(agentId, runId));
    if (!agentId) add(queryKeys.agents(projectId));
    return keys;
  }

  if (type.startsWith("agent.")) {
    add(queryKeys.agents(projectId));
    const agentId = resourceId(event);
    if (agentId) add(queryKeys.agent(agentId));
    return keys;
  }

  if (type.startsWith("function_execution.")) {
    const functionId = stringValue(data.function_id);
    const executionId = resourceId(event);
    if (functionId) add(queryKeys.functionExecutions(projectId, functionId));
    if (functionId && executionId) {
      add(queryKeys.functionExecution(projectId, functionId, executionId));
    }
    return keys;
  }

  if (type.startsWith("function_deployment.")) {
    const functionId = stringValue(data.function_id);
    const deploymentId = resourceId(event);
    if (functionId) {
      add(queryKeys.function(projectId, functionId));
      add(queryKeys.functionDeployments(projectId, functionId));
    }
    if (functionId && deploymentId) {
      add(queryKeys.functionDeployment(projectId, functionId, deploymentId));
    }
    return keys;
  }

  if (type.startsWith("site_deployment.")) {
    const siteId = stringValue(data.site_id);
    const deploymentId = resourceId(event);
    if (siteId) {
      add(queryKeys.site(projectId, siteId));
      add(queryKeys.siteDeployments(projectId, siteId));
    }
    if (siteId && deploymentId) {
      add(queryKeys.siteDeployment(projectId, siteId, deploymentId));
    }
    return keys;
  }

  if (type.startsWith("webhook.")) {
    const webhookId = stringValue(data.webhook_id);
    if (webhookId && type.startsWith("webhook.delivery.")) {
      add(queryKeys.webhookDeliveries(projectId, webhookId));
    }
    add(queryKeys.webhooks(projectId));
    return keys;
  }

  if (type.startsWith("messaging.")) {
    add(queryKeys.messagingProviders(projectId));
    add(queryKeys.messagingTopics(projectId));
    add(queryKeys.messagingMessages(projectId));
    return keys;
  }

  if (type.startsWith("database.") || type.startsWith("database_")) {
    add(queryKeys.databases(projectId));
    return keys;
  }

  if (type.startsWith("storage_")) {
    add(queryKeys.buckets(projectId));
    return keys;
  }

  if (type.startsWith("project.")) {
    add(queryKeys.project(projectId));
    add(queryKeys.audit("project", projectId));
  }

  if (type.startsWith("function.")) {
    add(queryKeys.functions(projectId));
    const functionId = resourceId(event);
    if (functionId) add(queryKeys.function(projectId, functionId));
    return keys;
  }

  if (type.startsWith("site.")) {
    add(queryKeys.sites(projectId));
    const siteId = resourceId(event);
    if (siteId) add(queryKeys.site(projectId, siteId));
    return keys;
  }
  return keys;
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
