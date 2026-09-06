"use client";

import { useQuery } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { queryKeys } from "@/api/query-keys";

const list = { limit: 100 } as const;

export function useCurrentAccount() {
  return useQuery({ queryKey: queryKeys.account, queryFn: async () => unwrap(await api.GET("/v1/account")), retry: false, staleTime: 60_000 });
}

export function useAccountSessions() {
  return useQuery({ queryKey: queryKeys.accountSessions, queryFn: async () => unwrap(await api.GET("/v1/account/sessions")), retry: false });
}

export function useOrganizations() {
  return useQuery({ queryKey: queryKeys.organizations, queryFn: async () => unwrap(await api.GET("/v1/organizations", { params: { query: list } })) });
}

export function useOrganization(organizationId: string | undefined) {
  return useQuery({ queryKey: queryKeys.organization(organizationId ?? ""), enabled: Boolean(organizationId), queryFn: async () => { const data = await unwrap(await api.GET("/v1/organizations", { params: { query: list } })); return data?.organizations.find((organization) => organization.id === organizationId); } });
}

export function useProjects(organizationId: string | undefined) {
  return useQuery({ queryKey: queryKeys.projects(organizationId ?? ""), enabled: Boolean(organizationId), queryFn: async () => unwrap(await api.GET("/v1/organizations/{organizationID}/projects", { params: { path: { organizationID: organizationId! }, query: list } })) });
}

export function useProject(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.project(projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}", { params: { path: { projectID: projectId! } } })) });
}

export function useOrganizationPlan(organizationId: string | undefined) {
  return useQuery({ queryKey: ["organization-plan", organizationId], enabled: Boolean(organizationId), queryFn: async () => unwrap(await api.GET("/v1/organizations/{organizationID}/plan", { params: { path: { organizationID: organizationId! } } })) });
}

export function useMemberships(organizationId: string | undefined) {
  return useQuery({ queryKey: ["memberships", organizationId], enabled: Boolean(organizationId), queryFn: async () => unwrap(await api.GET("/v1/organizations/{organizationID}/memberships", { params: { path: { organizationID: organizationId! }, query: list } })) });
}

export function useOrganizationAudit(organizationId: string | undefined) {
  return useQuery({ queryKey: queryKeys.audit("organization", organizationId ?? ""), enabled: Boolean(organizationId), queryFn: async () => unwrap(await api.GET("/v1/organizations/{organizationID}/audit-events", { params: { path: { organizationID: organizationId! }, query: list } })) });
}

export function useOrganizationTraces(organizationId: string | undefined) {
  return useQuery({ queryKey: queryKeys.traces("organization", organizationId ?? ""), enabled: Boolean(organizationId), queryFn: async () => unwrap(await api.GET("/v1/organizations/{organizationID}/traces", { params: { path: { organizationID: organizationId! }, query: list } })) });
}

export function useProjectAudit(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.audit("project", projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/audit-events", { params: { path: { projectID: projectId! }, query: list } })) });
}

export function useProjectTraces(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.traces("project", projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/traces", { params: { path: { projectID: projectId! }, query: list } })) });
}

export function useProjectUsage(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.usage(projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/usage", { params: { path: { projectID: projectId! } } })) });
}

export function useFunctions(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.functions(projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/functions", { params: { path: { projectID: projectId! }, query: list } })) });
}

export function useFunction(projectId: string | undefined, functionId: string | undefined) {
  return useQuery({ queryKey: queryKeys.function(projectId ?? "", functionId ?? ""), enabled: Boolean(projectId && functionId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/functions/{functionID}", { params: { path: { projectID: projectId!, functionID: functionId! } } })) });
}

export function useFunctionDeployments(projectId: string | undefined, functionId: string | undefined) {
  return useQuery({ queryKey: queryKeys.functionDeployments(projectId ?? "", functionId ?? ""), enabled: Boolean(projectId && functionId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/functions/{functionID}/deployments", { params: { path: { projectID: projectId!, functionID: functionId! }, query: list } })) });
}

export function useFunctionExecutions(projectId: string | undefined, functionId: string | undefined) {
  return useQuery({ queryKey: queryKeys.functionExecutions(projectId ?? "", functionId ?? ""), enabled: Boolean(projectId && functionId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/functions/{functionID}/executions", { params: { path: { projectID: projectId!, functionID: functionId! }, query: list } })) });
}

export function useFunctionDeployment(projectId: string | undefined, functionId: string | undefined, deploymentId: string | undefined) {
  return useQuery({ queryKey: ["function-deployment", projectId, functionId, deploymentId], enabled: Boolean(projectId && functionId && deploymentId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/functions/{functionID}/deployments/{deploymentID}", { params: { path: { projectID: projectId!, functionID: functionId!, deploymentID: deploymentId! } } })) });
}

export function useSites(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.sites(projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/sites", { params: { path: { projectID: projectId! }, query: list } })) });
}

export function useSite(projectId: string | undefined, siteId: string | undefined) {
  return useQuery({ queryKey: queryKeys.site(projectId ?? "", siteId ?? ""), enabled: Boolean(projectId && siteId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/sites/{siteID}", { params: { path: { projectID: projectId!, siteID: siteId! } } })) });
}

export function useSiteDeployments(projectId: string | undefined, siteId: string | undefined) {
  return useQuery({ queryKey: queryKeys.siteDeployments(projectId ?? "", siteId ?? ""), enabled: Boolean(projectId && siteId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/sites/{siteID}/deployments", { params: { path: { projectID: projectId!, siteID: siteId! }, query: list } })) });
}

export function useDatabases(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.databases(projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/databases", { params: { path: { projectID: projectId! }, query: list } })) });
}

export function useDatabase(projectId: string | undefined, databaseId: string | undefined) {
  return useQuery({ queryKey: queryKeys.database(projectId ?? "", databaseId ?? ""), enabled: Boolean(projectId && databaseId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/databases/{databaseID}", { params: { path: { projectID: projectId!, databaseID: databaseId! } } })) });
}

export function useDatabaseTables(projectId: string | undefined, databaseId: string | undefined) {
  return useQuery({ queryKey: queryKeys.tables(projectId ?? "", databaseId ?? ""), enabled: Boolean(projectId && databaseId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/databases/{databaseID}/tables", { params: { path: { projectID: projectId!, databaseID: databaseId! }, query: list } })) });
}

export function useDatabaseRows(projectId: string | undefined, databaseId: string | undefined, tableId: string | undefined, query?: { cursor?: string; search?: string }) {
  return useQuery({ queryKey: [...queryKeys.rows(projectId ?? "", databaseId ?? "", tableId ?? ""), query], enabled: Boolean(projectId && databaseId && tableId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/databases/{databaseID}/tables/{tableID}/rows", { params: { path: { projectID: projectId!, databaseID: databaseId!, tableID: tableId! }, query: { limit: 50, ...query } } })) });
}

export function useStorageBuckets(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.buckets(projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/storage/buckets", { params: { path: { projectID: projectId! }, query: list } })) });
}

export function useStorageBucket(projectId: string | undefined, bucketId: string | undefined) {
  return useQuery({ queryKey: queryKeys.bucket(projectId ?? "", bucketId ?? ""), enabled: Boolean(projectId && bucketId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/storage/buckets/{bucketID}", { params: { path: { projectID: projectId!, bucketID: bucketId! } } })) });
}

export function useStorageFiles(projectId: string | undefined, bucketId: string | undefined) {
  return useQuery({ queryKey: queryKeys.files(projectId ?? "", bucketId ?? ""), enabled: Boolean(projectId && bucketId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/storage/buckets/{bucketID}/files", { params: { path: { projectID: projectId!, bucketID: bucketId! }, query: list } })) });
}

export function useProjectUsers(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.users(projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/users", { params: { path: { projectID: projectId! }, query: list } })) });
}

export function useProjectAPIKeys(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.apiKeys(projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/api-keys", { params: { path: { projectID: projectId! }, query: list } })) });
}

export function useWebhooks(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.webhooks(projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/webhooks", { params: { path: { projectID: projectId! }, query: list } })) });
}

export function useWebhookDeliveries(projectId: string | undefined, webhookId: string | undefined) {
  return useQuery({ queryKey: queryKeys.webhookDeliveries(projectId ?? "", webhookId ?? ""), enabled: Boolean(projectId && webhookId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/webhooks/{webhookID}/deliveries", { params: { path: { projectID: projectId!, webhookID: webhookId! }, query: list } })) });
}

export function useAgents(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.agents(projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/agents", { params: { query: { ...list, project_id: projectId! } } })) });
}

export function useAgent(agentId: string | undefined) {
  return useQuery({ queryKey: queryKeys.agent(agentId ?? ""), enabled: Boolean(agentId), queryFn: async () => unwrap(await api.GET("/v1/agents/{agentID}", { params: { path: { agentID: agentId! } } })) });
}

export function useAgentCatalog() {
  return useQuery({ queryKey: queryKeys.agentCatalog, queryFn: async () => unwrap(await api.GET("/v1/agent-catalog")), staleTime: 300_000 });
}

export function useAgentRuns(agentId: string | undefined) {
  return useQuery({ queryKey: queryKeys.agentRuns(agentId ?? ""), enabled: Boolean(agentId), queryFn: async () => unwrap(await api.GET("/v1/agents/{agentID}/runs", { params: { path: { agentID: agentId! }, query: list } })) });
}

export function useAuthSettings(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.authSettings(projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/auth/settings", { params: { path: { projectID: projectId! } } })) });
}

export function useServiceLayout(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.serviceLayout(projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/service-layout", { params: { path: { projectID: projectId! } } })) });
}

export function useMessagingProviders(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.messagingProviders(projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/messaging/providers", { params: { path: { projectID: projectId! }, query: list } })) });
}

export function useMessagingTopics(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.messagingTopics(projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/messaging/topics", { params: { path: { projectID: projectId! }, query: list } })) });
}

export function useMessagingMessages(projectId: string | undefined) {
  return useQuery({ queryKey: queryKeys.messagingMessages(projectId ?? ""), enabled: Boolean(projectId), queryFn: async () => unwrap(await api.GET("/v1/projects/{projectID}/messaging/messages", { params: { path: { projectID: projectId! }, query: list } })) });
}
