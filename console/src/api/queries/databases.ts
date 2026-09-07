"use client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { type CursorQuery, withCursorPage } from "@/api/pagination";
import type { paths } from "@/api/generated/schema";
import { queryKeys } from "@/api/query-keys";
import { fetchAllCursorPages } from "@/lib/cursor-pagination";

type DatabaseRowsQuery = NonNullable<
  paths["/v1/projects/{projectID}/databases/{databaseID}/tables/{tableID}/rows"]["get"]["parameters"]["query"]
>;

export function useDatabases(
  projectId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.databases(projectId ?? ""), params],
    enabled: Boolean(projectId),
    queryFn: async () =>
      unwrap(
        await api.GET("/v1/projects/{projectID}/databases", {
          params: { path: { projectID: projectId! }, query: params },
        }),
      ),
    placeholderData: keepPreviousData,
  });
}

export function useCanvasDatabases(projectId: string | undefined) {
  return useQuery({
    queryKey: [...queryKeys.databases(projectId ?? ""), "canvas"],
    enabled: Boolean(projectId),
    staleTime: 30_000,
    queryFn: () =>
      fetchAllCursorPages(
        (cursor) =>
          api
            .GET("/v1/projects/{projectID}/databases", {
              params: {
                path: { projectID: projectId! },
                query: withCursorPage(cursor ? { cursor } : undefined),
              },
            })
            .then(unwrap),
        (page) => page.databases,
      ),
  });
}

export function useDatabase(
  projectId: string | undefined,
  databaseId: string | undefined,
) {
  return useQuery({
    queryKey: queryKeys.database(projectId ?? "", databaseId ?? ""),
    enabled: Boolean(projectId && databaseId),
    queryFn: async () =>
      unwrap(
        await api.GET("/v1/projects/{projectID}/databases/{databaseID}", {
          params: { path: { projectID: projectId!, databaseID: databaseId! } },
        }),
      ),
  });
}

export function useDatabaseTables(
  projectId: string | undefined,
  databaseId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.tables(projectId ?? "", databaseId ?? ""), params],
    enabled: Boolean(projectId && databaseId),
    queryFn: async () =>
      unwrap(
        await api.GET(
          "/v1/projects/{projectID}/databases/{databaseID}/tables",
          {
            params: {
              path: { projectID: projectId!, databaseID: databaseId! },
              query: params,
            },
          },
        ),
      ),
    placeholderData: keepPreviousData,
  });
}

export function useDatabaseBackups(
  projectId: string | undefined,
  databaseId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [
      ...queryKeys.databaseBackups(projectId ?? "", databaseId ?? ""),
      params,
    ],
    enabled: Boolean(projectId && databaseId),
    queryFn: async () =>
      unwrap(
        await api.GET(
          "/v1/projects/{projectID}/databases/{databaseID}/backups",
          {
            params: {
              path: { projectID: projectId!, databaseID: databaseId! },
              query: params,
            },
          },
        ),
      ),
    placeholderData: keepPreviousData,
  });
}

export function useDatabaseRows(
  projectId: string | undefined,
  databaseId: string | undefined,
  tableId: string | undefined,
  query?: Omit<DatabaseRowsQuery, "limit">,
) {
  const params = { limit: 50, ...query };
  return useQuery({
    queryKey: [
      ...queryKeys.rows(projectId ?? "", databaseId ?? "", tableId ?? ""),
      params,
    ],
    enabled: Boolean(projectId && databaseId && tableId),
    queryFn: async () =>
      unwrap(
        await api.GET(
          "/v1/projects/{projectID}/databases/{databaseID}/tables/{tableID}/rows",
          {
            params: {
              path: {
                projectID: projectId!,
                databaseID: databaseId!,
                tableID: tableId!,
              },
              query: params,
            },
          },
        ),
      ),
    placeholderData: keepPreviousData,
  });
}
