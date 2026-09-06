"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap, uploadMultipart } from "@/api/client";
import { queryKeys } from "@/api/query-keys";
import type { components } from "@/api/generated/schema";

export function useLogin() {
  return useMutation({ mutationFn: async (body: { email: string; password: string }) => unwrap(await api.POST("/v1/sessions/email-password", { body })) });
}

export function useRegister() {
  return useMutation({ mutationFn: async (body: { email: string; password: string }) => unwrap(await api.POST("/v1/account/registrations", { body })) });
}

export function useRecoveryRequest() {
  return useMutation({ mutationFn: async (body: { email: string }) => unwrap(await api.POST("/v1/account/recovery", { body })) });
}

export function useLogout() {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async () => unwrap(await api.DELETE("/v1/session")), onSuccess: () => queryClient.clear() });
}

export function useRevokeAccountSession() {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (sessionId: string) => unwrap(await api.DELETE("/v1/account/sessions/{sessionID}", { params: { path: { sessionID: sessionId } } })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.accountSessions }) });
}

export function useRevokeOtherAccountSessions() {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async () => unwrap(await api.DELETE("/v1/account/sessions")), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.accountSessions }) });
}

export function useUpdateAccountPassword() {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: components["schemas"]["UpdateAccountPasswordRequest"]) => unwrap(await api.PATCH("/v1/account/password", { body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.accountSessions }) });
}

export function useCreateOrganization() {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: { name: string; slug: string }) => unwrap(await api.POST("/v1/organizations", { body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.organizations }) });
}

export function useCreateProject(organizationId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: { name: string }) => unwrap(await api.POST("/v1/organizations/{organizationID}/projects", { params: { path: { organizationID: organizationId } }, body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.projects(organizationId) }) });
}

export function useCreateFunction(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: components["schemas"]["CreateFunctionRequest"]) => unwrap(await api.POST("/v1/projects/{projectID}/functions", { params: { path: { projectID: projectId } }, body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.functions(projectId) }) });
}

export function useCreateSite(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: components["schemas"]["CreateSiteRequest"]) => unwrap(await api.POST("/v1/projects/{projectID}/sites", { params: { path: { projectID: projectId } }, body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.sites(projectId) }) });
}

export function useCreateDatabase(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: { name: string }) => unwrap(await api.POST("/v1/projects/{projectID}/databases", { params: { path: { projectID: projectId } }, body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.databases(projectId) }) });
}

export function useCreateBucket(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: { name: string; file_security: boolean }) => unwrap(await api.POST("/v1/projects/{projectID}/storage/buckets", { params: { path: { projectID: projectId } }, body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.buckets(projectId) }) });
}

export function useCreateProjectUser(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: { email: string; password: string; name?: string | null }) => unwrap(await api.POST("/v1/projects/{projectID}/users", { params: { path: { projectID: projectId } }, body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.users(projectId) }) });
}

export function useCreateWebhook(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: components["schemas"]["CreateWebhookRequest"]) => unwrap(await api.POST("/v1/projects/{projectID}/webhooks", { params: { path: { projectID: projectId } }, body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.webhooks(projectId) }) });
}

export function useCreateAPIKey(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: components["schemas"]["CreateProjectAPIKeyRequest"]) => unwrap(await api.POST("/v1/projects/{projectID}/api-keys", { params: { path: { projectID: projectId } }, body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.apiKeys(projectId) }) });
}

export function useCreateAgent(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: components["schemas"]["CreateAgentRequest"]) => unwrap(await api.POST("/v1/agents", { body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.agents(projectId) }) });
}

export function useCreateAgentRun(agentId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: { prompt: string }) => unwrap(await api.POST("/v1/agents/{agentID}/runs", { params: { path: { agentID: agentId } }, body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.agentRuns(agentId) }) });
}

export function useUpdateAuthSettings(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: { registration_enabled?: boolean; cors_origins?: string[] }) => unwrap(await api.PATCH("/v1/projects/{projectID}/auth/settings", { params: { path: { projectID: projectId } }, body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.authSettings(projectId) }) });
}

export function useReplaceServiceLayout(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: components["schemas"]["ReplaceProjectServiceLayoutRequest"]) => unwrap(await api.PUT("/v1/projects/{projectID}/service-layout", { params: { path: { projectID: projectId } }, body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.serviceLayout(projectId) }) });
}

export function useActivateFunctionDeployment(projectId: string, functionId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (deploymentId: string) => unwrap(await api.POST("/v1/projects/{projectID}/functions/{functionID}/deployments/{deploymentID}/activate", { params: { path: { projectID: projectId, functionID: functionId, deploymentID: deploymentId } } })), onSuccess: () => { queryClient.invalidateQueries({ queryKey: queryKeys.function(projectId, functionId) }); queryClient.invalidateQueries({ queryKey: queryKeys.functionDeployments(projectId, functionId) }); } });
}

export function useUploadFunctionDeployment(projectId: string, functionId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async ({ file }: { file: File }) => { const form = new FormData(); form.append("source", file); return uploadMultipart(`/v1/projects/${projectId}/functions/${functionId}/deployments`, form); }, onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.functionDeployments(projectId, functionId) }) });
}

export function useUploadSiteDeployment(projectId: string, siteId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async ({ file, activate = true }: { file: File; activate?: boolean }) => { const form = new FormData(); form.append("source", file); form.append("activate", activate ? "true" : "false"); return uploadMultipart(`/v1/projects/${projectId}/sites/${siteId}/deployments`, form); }, onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.siteDeployments(projectId, siteId) }) });
}
