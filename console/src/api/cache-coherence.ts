import type { QueryClient } from "@tanstack/react-query";
import { queryKeys } from "@/api/query-keys";

export type CacheQueryKey = readonly unknown[];

/**
 * A domain change that can be translated into the queries whose source of
 * truth is now stale. Keeping this semantic input shared means realtime and
 * mutation adapters cannot quietly grow different invalidation policies.
 */
export type CacheChange =
  | { kind: "agent"; projectId: string; agentId?: string }
  | {
      kind: "agent-run";
      projectId?: string;
      agentId?: string;
      runId?: string;
      includeAgent?: boolean;
      includeAgentDetail?: boolean;
    }
  | { kind: "function"; projectId: string; functionId?: string }
  | {
      kind: "function-variable";
      projectId: string;
      functionId: string;
    }
  | {
      kind: "function-deployment";
      projectId: string;
      functionId: string;
      deploymentId?: string;
    }
  | {
      kind: "function-execution";
      projectId: string;
      functionId?: string;
      executionId?: string;
    }
  | { kind: "site"; projectId: string; siteId?: string }
  | {
      kind: "site-deployment";
      projectId: string;
      siteId: string;
      deploymentId?: string;
      includeScope?: boolean;
    }
  | {
      kind: "webhook";
      projectId: string;
      webhookId?: string;
      includeDetail?: boolean;
      delivery?: boolean;
    }
  | { kind: "messaging"; projectId: string }
  | { kind: "database"; projectId: string; databaseId?: string }
  | {
      kind: "storage-bucket";
      projectId: string;
      bucketId?: string;
      includeDetail?: boolean;
    }
  | { kind: "project"; projectId: string };

function addKey(keys: CacheQueryKey[], key: CacheQueryKey) {
  if (
    !keys.some((candidate) => JSON.stringify(candidate) === JSON.stringify(key))
  ) {
    keys.push(key);
  }
}

export function invalidationKeysFor(change: CacheChange): CacheQueryKey[] {
  const keys: CacheQueryKey[] = [];

  switch (change.kind) {
    case "agent":
      addKey(keys, queryKeys.agents(change.projectId));
      if (change.agentId) addKey(keys, queryKeys.agent(change.agentId));
      return keys;

    case "agent-run":
      if (change.agentId) addKey(keys, queryKeys.agentRuns(change.agentId));
      if (change.agentId && change.runId) {
        addKey(keys, queryKeys.agentRun(change.agentId, change.runId));
      }
      if ((!change.agentId || change.includeAgent) && change.projectId) {
        addKey(keys, queryKeys.agents(change.projectId));
      }
      if (change.agentId && change.includeAgentDetail) {
        addKey(keys, queryKeys.agent(change.agentId));
      }
      return keys;

    case "function":
      addKey(keys, queryKeys.functions(change.projectId));
      if (change.functionId) {
        addKey(keys, queryKeys.function(change.projectId, change.functionId));
      }
      return keys;

    case "function-variable":
      addKey(
        keys,
        queryKeys.functionVariables(change.projectId, change.functionId),
      );
      return keys;

    case "function-deployment":
      addKey(keys, queryKeys.function(change.projectId, change.functionId));
      addKey(
        keys,
        queryKeys.functionDeployments(change.projectId, change.functionId),
      );
      if (change.deploymentId) {
        addKey(
          keys,
          queryKeys.functionDeployment(
            change.projectId,
            change.functionId,
            change.deploymentId,
          ),
        );
      }
      return keys;

    case "function-execution":
      if (change.functionId) {
        addKey(
          keys,
          queryKeys.functionExecutions(change.projectId, change.functionId),
        );
      }
      if (change.functionId && change.executionId) {
        addKey(
          keys,
          queryKeys.functionExecution(
            change.projectId,
            change.functionId,
            change.executionId,
          ),
        );
      }
      return keys;

    case "site":
      addKey(keys, queryKeys.sites(change.projectId));
      if (change.siteId) {
        addKey(keys, queryKeys.site(change.projectId, change.siteId));
      }
      return keys;

    case "site-deployment":
      addKey(keys, queryKeys.site(change.projectId, change.siteId));
      addKey(keys, queryKeys.siteDeployments(change.projectId, change.siteId));
      if (change.deploymentId) {
        addKey(
          keys,
          queryKeys.siteDeployment(
            change.projectId,
            change.siteId,
            change.deploymentId,
          ),
        );
      }
      if (change.includeScope || !change.deploymentId) {
        addKey(
          keys,
          queryKeys.siteDeploymentScope(change.projectId, change.siteId),
        );
      }
      return keys;

    case "webhook":
      if (change.delivery && change.webhookId) {
        addKey(
          keys,
          queryKeys.webhookDeliveries(change.projectId, change.webhookId),
        );
      }
      addKey(keys, queryKeys.webhooks(change.projectId));
      if (change.includeDetail && change.webhookId) {
        addKey(keys, queryKeys.webhook(change.projectId, change.webhookId));
      }
      return keys;

    case "messaging":
      addKey(keys, queryKeys.messagingProviders(change.projectId));
      addKey(keys, queryKeys.messagingTopics(change.projectId));
      addKey(keys, queryKeys.messagingMessages(change.projectId));
      return keys;

    case "database":
      addKey(keys, queryKeys.databases(change.projectId));
      if (change.databaseId) {
        addKey(keys, queryKeys.database(change.projectId, change.databaseId));
      }
      return keys;

    case "storage-bucket":
      addKey(keys, queryKeys.buckets(change.projectId));
      if (change.includeDetail && change.bucketId) {
        addKey(keys, queryKeys.bucket(change.projectId, change.bucketId));
      }
      return keys;

    case "project":
      addKey(keys, queryKeys.project(change.projectId));
      addKey(keys, queryKeys.audit("project", change.projectId));
      return keys;
  }
}

export async function applyCacheChanges(
  queryClient: Pick<QueryClient, "invalidateQueries">,
  changes: readonly CacheChange[],
) {
  const keys = changes.flatMap(invalidationKeysFor);
  const uniqueKeys = keys.filter(
    (key, index) =>
      keys.findIndex(
        (candidate) => JSON.stringify(candidate) === JSON.stringify(key),
      ) === index,
  );

  await Promise.all(
    uniqueKeys.map((queryKey) => queryClient.invalidateQueries({ queryKey })),
  );
}
