"use client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, cancellableQuery, unwrap } from "@/api/client";
import { type CursorQuery, withCursorPage } from "@/api/pagination";
import { queryKeys } from "@/api/query-keys";
import { findCursorItem } from "@/lib/cursor-pagination";

async function findOrganizationById(
  organizationId: string,
  signal: AbortSignal,
) {
  return findCursorItem(
    (cursor) =>
      api
        .GET("/v1/organizations", {
          params: { query: withCursorPage(cursor ? { cursor } : undefined) },
          signal,
        })
        .then(unwrap),
    (page) => page.organizations,
    (organization) => organization.id === organizationId,
  );
}

type ListQueryOptions = {
  enabled?: boolean;
};

export function useOrganizations(
  query?: CursorQuery,
  options?: ListQueryOptions,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.organizations, params],
    enabled: options?.enabled ?? true,
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/organizations", { params: { query: params }, signal }),
    ),
    placeholderData: keepPreviousData,
  });
}

export function useOrganization(organizationId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.organization(organizationId ?? ""),
    enabled: Boolean(organizationId),
    queryFn: ({ signal }) => findOrganizationById(organizationId!, signal),
    staleTime: 60_000,
  });
}

export function useOrganizationPlan(organizationId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.organizationPlan(organizationId),
    enabled: Boolean(organizationId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/organizations/{organizationID}/plan", {
        params: { path: { organizationID: organizationId! } },
        signal,
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
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/organizations/{organizationID}/memberships", {
        params: { path: { organizationID: organizationId! }, query: params },
        signal,
      }),
    ),
    placeholderData: keepPreviousData,
  });
}

export function useOrganizationInvitations(
  organizationId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.invitations(organizationId), params],
    enabled: Boolean(organizationId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/organizations/{organizationID}/invitations", {
        params: { path: { organizationID: organizationId! }, query: params },
        signal,
      }),
    ),
    placeholderData: keepPreviousData,
  });
}
