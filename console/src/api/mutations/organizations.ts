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
