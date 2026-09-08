"use client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ApiError, api, unwrap } from "@/api/client";
import { queryKeys } from "@/api/query-keys";
import type { components } from "@/api/generated/schema";
import { isSlug, toSlug } from "@/lib/utils";

export function useCreateOrganization() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["CreateOrganizationRequest"],
    ) => unwrap(await api.POST("/v1/organizations", { body })),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: queryKeys.organizations }),
  });
}

export function useCreateProject(organizationId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["CreateProjectRequest"]) => {
      const name = toSlug(body.name);
      if (!isSlug(name))
        throw new ApiError(
          "Project name must become a 2–63 character slug using lowercase letters, numbers, or hyphens.",
          422,
          "validation_error",
        );
      return unwrap(
        await api.POST("/v1/organizations/{organizationID}/projects", {
          params: { path: { organizationID: organizationId } },
          body: { ...body, name },
        }),
      );
    },
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.projects(organizationId),
      }),
  });
}

export function useCreateOrganizationMembership(organizationId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["CreateOrganizationMembershipRequest"],
    ) =>
      unwrap(
        await api.POST("/v1/organizations/{organizationID}/memberships", {
          params: { path: { organizationID: organizationId } },
          body,
        }),
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.memberships(organizationId),
      }),
  });
}

export function useUpdateOrganizationMembership(organizationId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({
      accountId,
      body,
    }: {
      accountId: string;
      body: components["schemas"]["UpdateOrganizationMembershipRequest"];
    }) =>
      unwrap(
        await api.PATCH(
          "/v1/organizations/{organizationID}/memberships/{accountID}",
          {
            params: {
              path: { organizationID: organizationId, accountID: accountId },
            },
            body,
          },
        ),
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.memberships(organizationId),
      }),
  });
}

export function useDeleteOrganizationMembership(organizationId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (accountId: string) =>
      unwrap(
        await api.DELETE(
          "/v1/organizations/{organizationID}/memberships/{accountID}",
          {
            params: {
              path: { organizationID: organizationId, accountID: accountId },
            },
          },
        ),
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.memberships(organizationId),
      }),
  });
}

export function useCreateOrganizationInvitation(organizationId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["CreateOrganizationInvitationRequest"],
    ) =>
      unwrap(
        await api.POST("/v1/organizations/{organizationID}/invitations", {
          params: { path: { organizationID: organizationId } },
          body,
        }),
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.invitations(organizationId),
      }),
  });
}

export function useRevokeOrganizationInvitation(organizationId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (invitationId: string) =>
      unwrap(
        await api.DELETE(
          "/v1/organizations/{organizationID}/invitations/{invitationID}",
          {
            params: {
              path: {
                organizationID: organizationId,
                invitationID: invitationId,
              },
            },
          },
        ),
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.invitations(organizationId),
      }),
  });
}

export function useAcceptOrganizationInvitation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["AcceptOrganizationInvitationRequest"],
    ) =>
      unwrap(await api.POST("/v1/organization-invitations/accept", { body })),
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.organizations });
      const organizationId = result?.membership.organization_id;
      if (organizationId)
        void queryClient.invalidateQueries({
          queryKey: queryKeys.memberships(organizationId),
        });
    },
  });
}
