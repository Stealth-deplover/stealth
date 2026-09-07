"use client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { type CursorQuery, withCursorPage } from "@/api/pagination";
import { queryKeys } from "@/api/query-keys";
import { findCursorItem } from "@/lib/cursor-pagination";

async function findOrganizationById(organizationId: string) {
  return findCursorItem(
    (cursor) =>
      api
        .GET("/v1/organizations", {
          params: { query: withCursorPage(cursor ? { cursor } : undefined) },
        })
        .then(unwrap),
    (page) => page.organizations,
    (organization) => organization.id === organizationId,
  );
}

export function useOrganizations(query?: CursorQuery) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.organizations, params],
    queryFn: async () =>
      unwrap(await api.GET("/v1/organizations", { params: { query: params } })),
    placeholderData: keepPreviousData,
  });
}

export function useOrganization(organizationId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.organization(organizationId ?? ""),
    enabled: Boolean(organizationId),
    queryFn: () => findOrganizationById(organizationId!),
    staleTime: 60_000,
  });
}

export function useOrganizationPlan(organizationId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.organizationPlan(organizationId),
    enabled: Boolean(organizationId),
    queryFn: async () =>
      unwrap(
        await api.GET("/v1/organizations/{organizationID}/plan", {
          params: { path: { organizationID: organizationId! } },
        }),
      ),
  });
}

export function useMemberships(
  organizationId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.memberships(organizationId), params],
    enabled: Boolean(organizationId),
    queryFn: async () =>
      unwrap(
        await api.GET("/v1/organizations/{organizationID}/memberships", {
          params: { path: { organizationID: organizationId! }, query: params },
        }),
      ),
    placeholderData: keepPreviousData,
  });
}
