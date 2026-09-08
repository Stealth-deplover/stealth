export const queryKeys = {
  account: ["account"] as const,
  accountSessions: ["account-sessions"] as const,
  organizations: ["organizations"] as const,
  organization: (organizationId: string) =>
    ["organization", organizationId] as const,
  organizationPlan: (organizationId: string | undefined) =>
    ["organization-plan", organizationId] as const,
  memberships: (organizationId: string | undefined) =>
    ["memberships", organizationId] as const,
  invitations: (organizationId: string | undefined) =>
    ["invitations", organizationId] as const,
  projects: (organizationId: string) => ["projects", organizationId] as const,
  project: (projectId: string) => ["project", projectId] as const,
  usage: (projectId: string) => ["usage", projectId] as const,
  audit: (scope: string, id: string) => [scope, id, "audit"] as const,
  traces: (scope: string, id: string) => [scope, id, "traces"] as const,
  functions: (projectId: string) => ["functions", projectId] as const,
  function: (projectId: string, functionId: string) =>
    ["function", projectId, functionId] as const,
  functionDeployments: (projectId: string, functionId: string) =>
    ["function-deployments", projectId, functionId] as const,
  functionExecutions: (projectId: string, functionId: string) =>
    ["function-executions", projectId, functionId] as const,
  functionExecution: (
    projectId: string,
    functionId: string,
    executionId: string,
  ) => ["function-execution", projectId, functionId, executionId] as const,
  functionVariables: (projectId: string, functionId: string) =>
    ["function-variables", projectId, functionId] as const,
  functionDeployment: (
    projectId: string,
    functionId: string,
    deploymentId: string,
  ) => ["function-deployment", projectId, functionId, deploymentId] as const,
  sites: (projectId: string) => ["sites", projectId] as const,
  site: (projectId: string, siteId: string) =>
    ["site", projectId, siteId] as const,
  siteDeployments: (projectId: string, siteId: string) =>
    ["site-deployments", projectId, siteId] as const,
  siteDeployment: (projectId: string, siteId: string, deploymentId: string) =>
    ["site-deployment", projectId, siteId, deploymentId] as const,
  siteDeploymentScope: (projectId: string, siteId: string) =>
    ["site-deployment", projectId, siteId] as const,
  databases: (projectId: string) => ["databases", projectId] as const,
  database: (projectId: string, databaseId: string) =>
    ["database", projectId, databaseId] as const,
  tables: (projectId: string, databaseId: string) =>
    ["tables", projectId, databaseId] as const,
  table: (projectId: string, databaseId: string, tableId: string) =>
    ["table", projectId, databaseId, tableId] as const,
  tableScope: (projectId: string, databaseId: string) =>
    ["table", projectId, databaseId] as const,
  columns: (projectId: string, databaseId: string, tableId: string) =>
    ["columns", projectId, databaseId, tableId] as const,
  columnsScope: (projectId: string, databaseId: string) =>
    ["columns", projectId, databaseId] as const,
  indexes: (projectId: string, databaseId: string, tableId: string) =>
    ["indexes", projectId, databaseId, tableId] as const,
  indexesScope: (projectId: string, databaseId: string) =>
    ["indexes", projectId, databaseId] as const,
  row: (
    projectId: string,
    databaseId: string,
    tableId: string,
    rowId: string,
  ) => ["row", projectId, databaseId, tableId, rowId] as const,
  rowScope: (projectId: string, databaseId: string, tableId: string) =>
    ["row", projectId, databaseId, tableId] as const,
  rowDatabaseScope: (projectId: string, databaseId: string) =>
    ["row", projectId, databaseId] as const,
  databaseBackups: (projectId: string, databaseId: string) =>
    ["database-backups", projectId, databaseId] as const,
  rows: (projectId: string, databaseId: string, tableId: string) =>
    ["rows", projectId, databaseId, tableId] as const,
  rowsScope: (projectId: string, databaseId: string) =>
    ["rows", projectId, databaseId] as const,
  buckets: (projectId: string) => ["storage", projectId] as const,
  bucket: (projectId: string, bucketId: string) =>
    ["bucket", projectId, bucketId] as const,
  files: (projectId: string, bucketId: string) =>
    ["files", projectId, bucketId] as const,
  file: (projectId: string, bucketId: string, fileId: string) =>
    ["file", projectId, bucketId, fileId] as const,
  fileScope: (projectId: string, bucketId: string) =>
    ["file", projectId, bucketId] as const,
  users: (projectId: string) => ["users", projectId] as const,
  projectUser: (projectId: string, userId: string) =>
    ["project-user", projectId, userId] as const,
  apiKeys: (projectId: string) => ["api-keys", projectId] as const,
  apiKey: (projectId: string, keyId: string) =>
    ["api-key", projectId, keyId] as const,
  webhooks: (projectId: string) => ["webhooks", projectId] as const,
  webhook: (projectId: string, webhookId: string) =>
    ["webhook", projectId, webhookId] as const,
  webhookDeliveries: (projectId: string, webhookId: string) =>
    ["webhook-deliveries", projectId, webhookId] as const,
  agents: (projectId: string) => ["agents", projectId] as const,
  agent: (agentId: string) => ["agent", agentId] as const,
  agentCatalog: ["agent-catalog"] as const,
  agentRuns: (agentId: string) => ["agent-runs", agentId] as const,
  agentRun: (agentId: string, runId: string) =>
    ["agent-run", agentId, runId] as const,
  messagingProviders: (projectId: string) =>
    ["messaging-providers", projectId] as const,
  messagingTopics: (projectId: string) =>
    ["messaging-topics", projectId] as const,
  messagingMessages: (projectId: string) =>
    ["messaging-messages", projectId] as const,
  authSettings: (projectId: string) => ["auth-settings", projectId] as const,
  serviceLayout: (projectId: string) => ["service-layout", projectId] as const,
} as const;
