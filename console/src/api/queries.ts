"use client";

import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { type AgentListQuery, type CursorQuery, withAgentPage, withCursorPage } from "@/api/pagination";
import { queryKeys } from "@/api/query-keys";
import type { paths } from "@/api/generated/schema";
import { fetchAllCursorPages, findCursorItem } from "@/lib/cursor-pagination";

type DatabaseRowsQuery = NonNullable<paths["/v1/projects/{projectID}/databases/{databaseID}/tables/{tableID}/rows"]["get"]["parameters"]["query"]>;
type ProjectAgentQuery = Omit<AgentListQuery, "project_id">;

async function findOrganizationById(organizationId: string) {
  return findCursorItem(
    (cursor) => api.GET("/v1/organizations", { params: { query: withCursorPage(cursor ? { cursor } : undefined) } }).then(unwrap),
    (page) => page.organizations,
    (organization) => organization.id === organizationId,
  );
}

export function useCurrentAccount() {
  return useQuery({ queryKey: queryKeys.account, queryFn: async () => unwrap(await api.GET("/v1/account")), retry: false, staleTime: 60_000 });
}

export function useAccountSessions() {
  return useQuery({ queryKey: queryKeys.accountSessions, queryFn: async () => unwrap(await api.GET("/v1/account/sessions")), retry: false });
}

export function useOrganizations(query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.organizations, params], queryFn: async () => unwrap(await api.GET("/v1/organizations", { params: { query: params } })), placeholderData: keepPreviousData });
}

export function useOrganization(organizationId: string | undefined) {
  return useQuery({ queryKey: queryKeys.organization(organizationId ?? ""), enabled: Boolean(organizationId), queryFn: () => findOrganizationById(organizationId!), staleTime: 60_000 });
}

export function useProjects(organizationId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.projects(organizationId ?? ""), params], enabled: Boolean(organizationId), queryFn: async () => unwrap(await api.GET("/v1/organizations/{organizationID}/projects", { params: { path: { organizationID: organizationId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useProject(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.project(projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}", { params: { path: { projectID: projectId! } } })) });
}

export function useOrganizationPlan(organizationId: string | undefined) {
  return useQuery({ queryKey: ["organization-plan", organizationId], enabled: Boolean(organizationId), queryFn: async () => unwrap(await api.GET("/v1/organizations/{organizationID}/plan", { params: { path: { organizationID: organizationId! } } })) });
}

export function useMemberships(organizationId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: ["memberships", organizationId, params], enabled: Boolean(organizationId), queryFn: async () => unwrap(await api.GET("/v1/organizations/{organizationID}/memberships", { params: { path: { organizationID: organizationId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useOrganizationAudit(organizationId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.audit("organization", organizationId ?? ""), params], enabled: Boolean(organizationId), queryFn: async () => unwrap(await api.GET("/v1/organizations/{organizationID}/audit-events", { params: { path: { organizationID: organizationId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useOrganizationTraces(organizationId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.traces("organization", organizationId ?? ""), params], enabled: Boolean(organizationId), queryFn: async () => unwrap(await api.GET("/v1/organizations/{organizationID}/traces", { params: { path: { organizationID: organizationId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useProjectAudit(projectId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.audit("project", projectId ?? ""), params], enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/audit-events", { params: { path: { projectID: projectId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useProjectTraces(projectId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.traces("project", projectId ?? ""), params], enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/traces", { params: { path: { projectID: projectId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useProjectUsage(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.usage(projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/usage", { params: { path: { projectID: projectId! } } })) });
}

export function useFunctions(projectId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.functions(projectId ?? ""), params], enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/functions", { params: { path: { projectID: projectId! }, query: params } })), placeholderData: keepPreviousData });
}

/**
 * The Services canvas is an intentional exception to table pagination: it needs
 * every resource node to render a truthful topology. It follows the API cursor
 * until exhaustion instead of requesting an arbitrary large first page.
 */
export function useCanvasFunctions(projectId: string | undefined) {
  return useQuery({
    queryKey: [...queryKeys.functions(projectId ?? ""), "canvas"],
    enabled: Boolean(projectId),
    staleTime: 30_000,
    queryFn: () => fetchAllCursorPages(
      (cursor) => api.GET("/v1/projects/{projectID}/functions", { params: { path: { projectID: projectId! }, query: withCursorPage(cursor ? { cursor } : undefined) } }).then(unwrap),
      (page) => page.functions,
    ),
  });
}

export function useFunction(projectId: string | undefined, functionId: string | undefined) {
  return useQuery({ queryKey: queryKeys.function(projectId ?? "", functionId ?? ""), enabled: Boolean(projectId && functionId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/functions/{functionID}", { params: { path: { projectID: projectId!, functionID: functionId! } } })) });
}

export function useFunctionDeployments(projectId: string | undefined, functionId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.functionDeployments(projectId ?? "", functionId ?? ""), params], enabled: Boolean(projectId && functionId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/functions/{functionID}/deployments", { params: { path: { projectID: projectId!, functionID: functionId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useFunctionExecutions(projectId: string | undefined, functionId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.functionExecutions(projectId ?? "", functionId ?? ""), params], enabled: Boolean(projectId && functionId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/functions/{functionID}/executions", { params: { path: { projectID: projectId!, functionID: functionId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useFunctionVariables(projectId: string | undefined, functionId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.functionVariables(projectId ?? "", functionId ?? ""), params], enabled: Boolean(projectId && functionId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/functions/{functionID}/variables", { params: { path: { projectID: projectId!, functionID: functionId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useFunctionDeployment(projectId: string | undefined, functionId: string | undefined, deploymentId: string | undefined) {
  return useQuery({ queryKey: ["function-deployment", projectId, functionId, deploymentId], enabled: Boolean(projectId && functionId && deploymentId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/functions/{functionID}/deployments/{deploymentID}", { params: { path: { projectID: projectId!, functionID: functionId!, deploymentID: deploymentId! } } })), refetchInterval: (query) => { const deployment = query.state.data?.deployment; return deployment && (deployment.status === "queued" || deployment.build_status === "running" || deployment.build_status === "queued") ? 3_000 : false; } });
}

export function useSites(projectId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.sites(projectId ?? ""), params], enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/sites", { params: { path: { projectID: projectId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useCanvasSites(projectId: string | undefined) {
  return useQuery({
    queryKey: [...queryKeys.sites(projectId ?? ""), "canvas"],
    enabled: Boolean(projectId),
    staleTime: 30_000,
    queryFn: () => fetchAllCursorPages(
      (cursor) => api.GET("/v1/projects/{projectID}/sites", { params: { path: { projectID: projectId! }, query: withCursorPage(cursor ? { cursor } : undefined) } }).then(unwrap),
      (page) => page.sites,
    ),
  });
}

export function useSite(projectId: string | undefined, siteId: string | undefined) {
  return useQuery({ queryKey: queryKeys.site(projectId ?? "", siteId ?? ""), enabled: Boolean(projectId && siteId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/sites/{siteID}", { params: { path: { projectID: projectId!, siteID: siteId! } } })) });
}

export function useSiteDeployments(projectId: string | undefined, siteId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.siteDeployments(projectId ?? "", siteId ?? ""), params], enabled: Boolean(projectId && siteId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/sites/{siteID}/deployments", { params: { path: { projectID: projectId!, siteID: siteId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useSiteDeployment(projectId: string | undefined, siteId: string | undefined, deploymentId: string | undefined) {
  return useQuery({ queryKey: queryKeys.siteDeployment(projectId ?? "", siteId ?? "", deploymentId ?? ""), enabled: Boolean(projectId && siteId && deploymentId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/sites/{siteID}/deployments/{deploymentID}", { params: { path: { projectID: projectId!, siteID: siteId!, deploymentID: deploymentId! } } })), refetchInterval: (query) => { const deployment = query.state.data?.deployment; return deployment && (deployment.status === "queued" || deployment.build_status === "running" || deployment.build_status === "queued" || deployment.build_status === "deferred") ? 3_000 : false; } });
}

export function useDatabases(projectId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.databases(projectId ?? ""), params], enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/databases", { params: { path: { projectID: projectId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useCanvasDatabases(projectId: string | undefined) {
  return useQuery({
    queryKey: [...queryKeys.databases(projectId ?? ""), "canvas"],
    enabled: Boolean(projectId),
    staleTime: 30_000,
    queryFn: () => fetchAllCursorPages(
      (cursor) => api.GET("/v1/projects/{projectID}/databases", { params: { path: { projectID: projectId! }, query: withCursorPage(cursor ? { cursor } : undefined) } }).then(unwrap),
      (page) => page.databases,
    ),
  });
}

export function useDatabase(projectId: string | undefined, databaseId: string | undefined) {
  return useQuery({ queryKey: queryKeys.database(projectId ?? "", databaseId ?? ""), enabled: Boolean(projectId && databaseId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/databases/{databaseID}", { params: { path: { projectID: projectId!, databaseID: databaseId! } } })) });
}

export function useDatabaseTables(projectId: string | undefined, databaseId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.tables(projectId ?? "", databaseId ?? ""), params], enabled: Boolean(projectId && databaseId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/databases/{databaseID}/tables", { params: { path: { projectID: projectId!, databaseID: databaseId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useDatabaseBackups(projectId: string | undefined, databaseId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.databaseBackups(projectId ?? "", databaseId ?? ""), params], enabled: Boolean(projectId && databaseId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/databases/{databaseID}/backups", { params: { path: { projectID: projectId!, databaseID: databaseId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useDatabaseRows(projectId: string | undefined, databaseId: string | undefined, tableId: string | undefined, query?: Omit<DatabaseRowsQuery, "limit">) {
  const params = { limit: 50, ...query };
  return useQuery({ queryKey: [...queryKeys.rows(projectId ?? "", databaseId ?? "", tableId ?? ""), params], enabled: Boolean(projectId && databaseId && tableId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/databases/{databaseID}/tables/{tableID}/rows", { params: { path: { projectID: projectId!, databaseID: databaseId!, tableID: tableId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useStorageBuckets(projectId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.buckets(projectId ?? ""), params], enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/storage/buckets", { params: { path: { projectID: projectId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useCanvasStorageBuckets(projectId: string | undefined) {
  return useQuery({
    queryKey: [...queryKeys.buckets(projectId ?? ""), "canvas"],
    enabled: Boolean(projectId),
    staleTime: 30_000,
    queryFn: () => fetchAllCursorPages(
      (cursor) => api.GET("/v1/projects/{projectID}/storage/buckets", { params: { path: { projectID: projectId! }, query: withCursorPage(cursor ? { cursor } : undefined) } }).then(unwrap),
      (page) => page.buckets,
    ),
  });
}

export function useStorageBucket(projectId: string | undefined, bucketId: string | undefined) {
  return useQuery({ queryKey: queryKeys.bucket(projectId ?? "", bucketId ?? ""), enabled: Boolean(projectId && bucketId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/storage/buckets/{bucketID}", { params: { path: { projectID: projectId!, bucketID: bucketId! } } })) });
}

export function useStorageFiles(projectId: string | undefined, bucketId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.files(projectId ?? "", bucketId ?? ""), params], enabled: Boolean(projectId && bucketId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/storage/buckets/{bucketID}/files", { params: { path: { projectID: projectId!, bucketID: bucketId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useProjectUsers(projectId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.users(projectId ?? ""), params], enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/users", { params: { path: { projectID: projectId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useProjectAPIKeys(projectId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.apiKeys(projectId ?? ""), params], enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/api-keys", { params: { path: { projectID: projectId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useWebhooks(projectId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.webhooks(projectId ?? ""), params], enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/webhooks", { params: { path: { projectID: projectId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useWebhook(projectId: string | undefined, webhookId: string | undefined) {
  return useQuery({ queryKey: queryKeys.webhook(projectId ?? "", webhookId ?? ""), enabled: Boolean(projectId && webhookId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/webhooks/{webhookID}", { params: { path: { projectID: projectId!, webhookID: webhookId! } } })) });
}

export function useWebhookDeliveries(projectId: string | undefined, webhookId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.webhookDeliveries(projectId ?? "", webhookId ?? ""), params], enabled: Boolean(projectId && webhookId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/webhooks/{webhookID}/deliveries", { params: { path: { projectID: projectId!, webhookID: webhookId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useAgents(projectId: string | undefined, query?: ProjectAgentQuery) {
  const params = withAgentPage({ ...query, project_id: projectId! });
  return useQuery({ queryKey: [...queryKeys.agents(projectId ?? ""), params], enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/agents", { params: { query: params } })), placeholderData: keepPreviousData });
}

export function useAgent(agentId: string | undefined) {
  return useQuery({ queryKey: queryKeys.agent(agentId ?? ""), enabled: Boolean(agentId), queryFn: async () => unwrap(await api.GET("/v1/agents/{agentID}", { params: { path: { agentID: agentId! } } })) });
}

export function useAgentCatalog() {
  return useQuery({ queryKey: queryKeys.agentCatalog, queryFn: async () => unwrap(await api.GET("/v1/agent-catalog")), staleTime: 300_000 });
}

export function useAgentRuns(agentId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.agentRuns(agentId ?? ""), params], enabled: Boolean(agentId), queryFn: async () => unwrap(await api.GET("/v1/agents/{agentID}/runs", { params: { path: { agentID: agentId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useAgentRun(agentId: string | undefined, runId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.agentRun(agentId ?? "", runId ?? ""),
    enabled: Boolean(agentId && runId),
    queryFn: async () => unwrap(await api.GET("/v1/agents/{agentID}/runs/{runID}", { params: { path: { agentID: agentId!, runID: runId! } } })),
    refetchInterval: (query) => {
      const status = query.state.data?.run.status;
      return status === "queued" || status === "running" ? 3_000 : false;
    },
  });
}

export function useAuthSettings(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.authSettings(projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/auth/settings", { params: { path: { projectID: projectId! } } })) });
}

export function useServiceLayout(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.serviceLayout(projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/service-layout", { params: { path: { projectID: projectId! } } })) });
}

export function useMessagingProviders(projectId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.messagingProviders(projectId ?? ""), params], enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/messaging/providers", { params: { path: { projectID: projectId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useMessagingTopics(projectId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.messagingTopics(projectId ?? ""), params], enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/messaging/topics", { params: { path: { projectID: projectId! }, query: params } })), placeholderData: keepPreviousData });
}

export function useMessagingMessages(projectId: string | undefined, query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({ queryKey: [...queryKeys.messagingMessages(projectId ?? ""), params], enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/messaging/messages", { params: { path: { projectID: projectId! }, query: params } })), placeholderData: keepPreviousData });
}
