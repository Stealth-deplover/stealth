import type { QueryClient } from "@tanstack/react-query";
import { queryKeys } from "@/api/query-keys";

export type CacheQueryKey = readonly unknown[];

/**
 * A domain change that can be translated into the queries whose source of
 * truth is now stale. Keeping this semantic input shared means realtime and
 * mutation adapters cannot quietly grow different invalidation policies.
 */
export type CacheChange =
  | { kind: "organizations" }
  | { kind: "organization-projects"; organizationId: string }
  | { kind: "organization-memberships"; organizationId: string }
  | { kind: "organization-invitations"; organizationId: string }
  | {
      kind: "account";
      includeOrganizations?: boolean;
      includeBootstrapStatus?: boolean;
    }
  | { kind: "account-sessions" }
  | { kind: "auth-settings"; projectId: string }
  | { kind: "service-layout"; projectId: string }
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
  | { kind: "database-table"; projectId: string; databaseId: string }
  | {
      kind: "database-table-schema";
      projectId: string;
      databaseId: string;
      tableId: string;
    }
  | {
      kind: "database-table-rows";
      projectId: string;
      databaseId: string;
      tableId: string;
    }
  | {
      kind: "database-backup";
      projectId: string;
      databaseId: string;
      restore?: boolean;
    }
  | {
      kind: "storage-bucket";
      projectId: string;
      bucketId?: string;
      includeDetail?: boolean;
    }
  | {
      kind: "storage-file";
      projectId: string;
      bucketId: string;
      operation: "upload" | "rename" | "delete";
    }
  | { kind: "project-user"; projectId: string; userId?: string }
  | { kind: "api-key"; projectId: string; keyId?: string }
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
    case "organizations":
      addKey(keys, queryKeys.organizations);
      return keys;

    case "organization-projects":
      addKey(keys, queryKeys.projects(change.organizationId));
      return keys;

    case "organization-memberships":
      addKey(keys, queryKeys.memberships(change.organizationId));
      return keys;

    case "organization-invitations":
      addKey(keys, queryKeys.invitations(change.organizationId));
      return keys;

    case "account":
      addKey(keys, queryKeys.account);
      if (change.includeOrganizations) addKey(keys, queryKeys.organizations);
      if (change.includeBootstrapStatus)
        addKey(keys, queryKeys.bootstrapStatus);
      return keys;

    case "account-sessions":
      addKey(keys, queryKeys.accountSessions);
      return keys;

    case "auth-settings":
      addKey(keys, queryKeys.authSettings(change.projectId));
      return keys;

    case "service-layout":
      addKey(keys, queryKeys.serviceLayout(change.projectId));
      return keys;

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

    case "database-table":
      addKey(keys, queryKeys.tables(change.projectId, change.databaseId));
      return keys;

    case "database-table-schema":
      addKey(
        keys,
        queryKeys.columns(
          change.projectId,
          change.databaseId,
          change.tableId,
        ),
      );
      addKey(
        keys,
        queryKeys.rows(change.projectId, change.databaseId, change.tableId),
      );
      addKey(
        keys,
        queryKeys.rowScope(change.projectId, change.databaseId, change.tableId),
      );
      return keys;

    case "database-table-rows":
      addKey(
        keys,
        queryKeys.rows(change.projectId, change.databaseId, change.tableId),
      );
      addKey(
        keys,
        queryKeys.rowScope(change.projectId, change.databaseId, change.tableId),
      );
      return keys;

    case "database-backup":
      if (change.restore) {
        addKey(
          keys,
          queryKeys.rowsScope(change.projectId, change.databaseId),
        );
        addKey(
          keys,
          queryKeys.rowDatabaseScope(change.projectId, change.databaseId),
        );
        addKey(
          keys,
          queryKeys.tableScope(change.projectId, change.databaseId),
        );
        addKey(
          keys,
          queryKeys.columnsScope(change.projectId, change.databaseId),
        );
        addKey(
          keys,
          queryKeys.indexesScope(change.projectId, change.databaseId),
        );
        addKey(keys, queryKeys.database(change.projectId, change.databaseId));
        addKey(keys, queryKeys.tables(change.projectId, change.databaseId));
      }
      addKey(
        keys,
        queryKeys.databaseBackups(change.projectId, change.databaseId),
      );
      return keys;

    case "storage-bucket":
      addKey(keys, queryKeys.buckets(change.projectId));
      if (change.includeDetail && change.bucketId) {
        addKey(keys, queryKeys.bucket(change.projectId, change.bucketId));
      }
      return keys;

    case "storage-file":
      addKey(keys, queryKeys.files(change.projectId, change.bucketId));
      if (change.operation === "rename") {
        addKey(keys, queryKeys.fileScope(change.projectId, change.bucketId));
      } else {
        addKey(keys, queryKeys.bucket(change.projectId, change.bucketId));
        addKey(keys, queryKeys.buckets(change.projectId));
      }
      return keys;

    case "project-user":
      addKey(keys, queryKeys.users(change.projectId));
      if (change.userId) {
        addKey(keys, queryKeys.projectUser(change.projectId, change.userId));
      }
      return keys;

    case "api-key":
      addKey(keys, queryKeys.apiKeys(change.projectId));
      if (change.keyId) {
        addKey(keys, queryKeys.apiKey(change.projectId, change.keyId));
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
