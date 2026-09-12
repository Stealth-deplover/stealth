"use client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { queryKeys } from "@/api/query-keys";
import { applyCacheChanges } from "@/api/cache-coherence";
import type { components } from "@/api/generated/schema";

export function useLogin() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["LoginRequest"]) =>
      unwrap(await api.POST("/v1/sessions/email-password", { body })),
    onSuccess: () =>
      applyCacheChanges(queryClient, [
        { kind: "account", includeOrganizations: true },
      ]),
  });
}

export function useRegister() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["RegisterRequest"]) =>
      unwrap(await api.POST("/v1/account/registrations", { body })),
    onSuccess: () =>
      applyCacheChanges(queryClient, [
        { kind: "account", includeOrganizations: true },
      ]),
  });
}

export function useVerifyBootstrapCode() {
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["VerifyBootstrapCodeRequest"],
    ) => unwrap(await api.POST("/v1/bootstrap/verify", { body })),
  });
}

export function useStartGitHubDeviceFlow() {
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["StartGitHubDeviceRequest"],
    ) => unwrap(await api.POST("/v1/bootstrap/github/device", { body })),
  });
}

export function usePollGitHubDeviceFlow() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["PollGitHubDeviceRequest"],
    ) => unwrap(await api.POST("/v1/bootstrap/github/poll", { body })),
    onSuccess: (result) => {
      if (result?.status !== "complete") return;
      return applyCacheChanges(queryClient, [
        {
          kind: "account",
          includeOrganizations: true,
          includeBootstrapStatus: true,
        },
      ]);
    },
  });
}

export function useRecoveryRequest() {
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["PasswordRecoveryRequest"],
    ) => unwrap(await api.POST("/v1/account/recovery", { body })),
  });
}

export function useSendAccountVerification() {
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["AuthVerificationRequest"] = {},
    ) => unwrap(await api.POST("/v1/account/verification", { body })),
  });
}

export function useConfirmAccountVerification() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["AuthTokenRequest"]) =>
      unwrap(await api.PUT("/v1/account/verification", { body })),
    onSuccess: () =>
      applyCacheChanges(queryClient, [{ kind: "account" }]),
  });
}

export function useConfirmAccountRecovery() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["PasswordResetRequest"]) =>
      unwrap(await api.PUT("/v1/account/recovery", { body })),
    onSuccess: () => queryClient.removeQueries({ queryKey: queryKeys.account }),
  });
}

export function useLogout() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () => unwrap(await api.DELETE("/v1/session")),
    onSuccess: () => queryClient.clear(),
  });
}

export function useRevokeAccountSession() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (sessionId: string) =>
      unwrap(
        await api.DELETE("/v1/account/sessions/{sessionID}", {
          params: { path: { sessionID: sessionId } },
        }),
      ),
    onSuccess: () =>
      applyCacheChanges(queryClient, [{ kind: "account-sessions" }]),
  });
}

export function useRevokeOtherAccountSessions() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () => unwrap(await api.DELETE("/v1/account/sessions")),
    onSuccess: () =>
      applyCacheChanges(queryClient, [{ kind: "account-sessions" }]),
  });
}

export function useUpdateAccountPassword() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["UpdateAccountPasswordRequest"],
    ) => unwrap(await api.PATCH("/v1/account/password", { body })),
    onSuccess: () =>
      applyCacheChanges(queryClient, [{ kind: "account-sessions" }]),
  });
}
