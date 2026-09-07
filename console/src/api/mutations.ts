"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ApiError, api, unwrap, uploadMultipart } from "@/api/client";
import { queryKeys } from "@/api/query-keys";
import type { components } from "@/api/generated/schema";
import { isSlug, toSlug } from "@/lib/utils";

export function useLogin() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["LoginRequest"]) => unwrap(await api.POST("/v1/sessions/email-password", { body })),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.account }),
        queryClient.invalidateQueries({ queryKey: queryKeys.organizations }),
      ]);
    },
  });
}

export function useRegister() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["RegisterRequest"]) => unwrap(await api.POST("/v1/account/registrations", { body })),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.account }),
        queryClient.invalidateQueries({ queryKey: queryKeys.organizations }),
      ]);
    },
  });
}

export function useRecoveryRequest() {
  return useMutation({ mutationFn: async (body: components["schemas"]["PasswordRecoveryRequest"]) => unwrap(await api.POST("/v1/account/recovery", { body })) });
}

export function useSendAccountVerification() {
  return useMutation({ mutationFn: async (body: components["schemas"]["AuthVerificationRequest"] = {}) => unwrap(await api.POST("/v1/account/verification", { body })) });
}

export function useConfirmAccountVerification() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["AuthTokenRequest"]) => unwrap(await api.PUT("/v1/account/verification", { body })),
    onSuccess: async () => queryClient.invalidateQueries({ queryKey: queryKeys.account }),
  });
}

export function useConfirmAccountRecovery() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["PasswordResetRequest"]) => unwrap(await api.PUT("/v1/account/recovery", { body })),
    onSuccess: () => queryClient.removeQueries({ queryKey: queryKeys.account }),
  });
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
  return useMutation({ mutationFn: async (body: components["schemas"]["CreateOrganizationRequest"]) => unwrap(await api.POST("/v1/organizations", { body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.organizations }) });
}

export function useCreateProject(organizationId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: components["schemas"]["CreateProjectRequest"]) => { const name = toSlug(body.name); if (!isSlug(name)) throw new ApiError("Project name must become a 2–63 character slug using lowercase letters, numbers, or hyphens.", 422, "validation_error"); return unwrap(await api.POST("/v1/organizations/{organizationID}/projects", { params: { path: { organizationID: organizationId } }, body: { ...body, name } })); }, onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.projects(organizationId) }) });
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
  return useMutation({ mutationFn: async (body: components["schemas"]["CreateDatabaseRequest"]) => unwrap(await api.POST("/v1/projects/{projectID}/databases", { params: { path: { projectID: projectId } }, body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.databases(projectId) }) });
}

export function useCreateDatabaseBackup(projectId: string, databaseId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async () => unwrap(await api.POST("/v1/projects/{projectID}/databases/{databaseID}/backups", { params: { path: { projectID: projectId, databaseID: databaseId }, query: { max_rows: 10000 } } })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.databaseBackups(projectId, databaseId) }) });
}

export function useDeleteDatabaseBackup(projectId: string, databaseId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (backupId: string) => unwrap(await api.DELETE("/v1/projects/{projectID}/databases/{databaseID}/backups/{backupID}", { params: { path: { projectID: projectId, databaseID: databaseId, backupID: backupId } } })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.databaseBackups(projectId, databaseId) }) });
}

export function useRestoreDatabaseBackup(projectId: string, databaseId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (backupId: string) => unwrap(await api.POST("/v1/projects/{projectID}/databases/{databaseID}/backups/{backupID}/restore", { params: { path: { projectID: projectId, databaseID: databaseId, backupID: backupId } } })), onSuccess: () => { queryClient.invalidateQueries({ queryKey: queryKeys.database(projectId, databaseId) }); queryClient.invalidateQueries({ queryKey: queryKeys.tables(projectId, databaseId) }); queryClient.invalidateQueries({ queryKey: queryKeys.databaseBackups(projectId, databaseId) }); } });
}

export function useCreateBucket(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: components["schemas"]["CreateStorageBucketRequest"]) => unwrap(await api.POST("/v1/projects/{projectID}/storage/buckets", { params: { path: { projectID: projectId } }, body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.buckets(projectId) }) });
}

export function useDeleteStorageFile(projectId: string, bucketId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (fileId: string) => unwrap(await api.DELETE("/v1/projects/{projectID}/storage/buckets/{bucketID}/files/{fileID}", { params: { path: { projectID: projectId, bucketID: bucketId, fileID: fileId } } })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.files(projectId, bucketId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.bucket(projectId, bucketId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.buckets(projectId) });
    },
  });
}

export function useCreateFunctionVariable(projectId: string, functionId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: components["schemas"]["CreateFunctionVariableRequest"]) => unwrap(await api.POST("/v1/projects/{projectID}/functions/{functionID}/variables", { params: { path: { projectID: projectId, functionID: functionId } }, body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.functionVariables(projectId, functionId) }) });
}

export function useDeleteFunctionVariable(projectId: string, functionId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (variableId: string) => unwrap(await api.DELETE("/v1/projects/{projectID}/functions/{functionID}/variables/{variableID}", { params: { path: { projectID: projectId, functionID: functionId, variableID: variableId } } })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.functionVariables(projectId, functionId) }) });
}

export function useCreateProjectUser(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: components["schemas"]["CreateProjectUserRequest"]) => unwrap(await api.POST("/v1/projects/{projectID}/users", { params: { path: { projectID: projectId } }, body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.users(projectId) }) });
}

export function useCreateWebhook(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: components["schemas"]["CreateWebhookRequest"]) => unwrap(await api.POST("/v1/projects/{projectID}/webhooks", { params: { path: { projectID: projectId } }, body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.webhooks(projectId) }) });
}

export function useRotateWebhookSecret(projectId: string, webhookId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () => unwrap(await api.POST("/v1/projects/{projectID}/webhooks/{webhookID}/rotate-secret", { params: { path: { projectID: projectId, webhookID: webhookId } } })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.webhooks(projectId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.webhook(projectId, webhookId) });
    },
  });
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
  return useMutation({ mutationFn: async (body: components["schemas"]["CreateAgentRunRequest"]) => unwrap(await api.POST("/v1/agents/{agentID}/runs", { params: { path: { agentID: agentId } }, body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.agentRuns(agentId) }) });
}

export function useCancelAgentRun(agentId: string, runId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () => unwrap(await api.POST("/v1/agents/{agentID}/runs/{runID}/cancel", { params: { path: { agentID: agentId, runID: runId } } })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.agentRun(agentId, runId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.agentRuns(agentId) });
    },
  });
}

export function useUpdateAuthSettings(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: async (body: components["schemas"]["UpdateProjectAuthSettingsRequest"]) => unwrap(await api.PATCH("/v1/projects/{projectID}/auth/settings", { params: { path: { projectID: projectId } }, body })), onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.authSettings(projectId) }) });
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

export function useActivateSiteDeployment(projectId: string, siteId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (deploymentId: string) => unwrap(await api.POST("/v1/projects/{projectID}/sites/{siteID}/deployments/{deploymentID}/activate", { params: { path: { projectID: projectId, siteID: siteId, deploymentID: deploymentId } } })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.site(projectId, siteId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.siteDeployments(projectId, siteId) });
      queryClient.invalidateQueries({ queryKey: ["site-deployment", projectId, siteId] });
    },
  });
}
