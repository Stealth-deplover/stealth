"use client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, cancellableQuery, unwrap } from "@/api/client";
import { type CursorQuery, withCursorPage } from "@/api/pagination";
import type { paths } from "@/api/generated/schema";
import { queryKeys } from "@/api/query-keys";
import { fetchAllCursorPages } from "@/lib/cursor-pagination";

export type DatabaseRowsQuery = NonNullable<
  paths["/v1/projects/{projectID}/databases/{databaseID}/tables/{tableID}/rows"]["get"]["parameters"]["query"]
>;

export function useDatabaseTable(
  projectId: string,
  databaseId: string,
  tableId: string,
) {
  return useQuery({
    queryKey: queryKeys.table(projectId, databaseId, tableId),
    queryFn: cancellableQuery((signal) =>
      api.GET(
        "/v1/projects/{projectID}/databases/{databaseID}/tables/{tableID}",
        {
          params: {
            path: {
              projectID: projectId,
              databaseID: databaseId,
              tableID: tableId,
            },
          },
          signal,
        },
      ),
    ),
  });
}

export function useDatabaseColumns(
  projectId: string,
  databaseId: string,
  tableId: string,
) {
  return useQuery({
    queryKey: queryKeys.columns(projectId, databaseId, tableId),
    queryFn: ({ signal }) =>
      fetchAllCursorPages(
        (cursor) =>
          api
            .GET(
              "/v1/projects/{projectID}/databases/{databaseID}/tables/{tableID}/columns",
              {
                params: {
                  path: {
                    projectID: projectId,
                    databaseID: databaseId,
                    tableID: tableId,
                  },
                  query: withCursorPage({ cursor }),
                },
                signal,
              },
            )
            .then(unwrap),
        (page) => page.columns,
        { signal },
      ),
  });
}

export function useDatabaseIndexes(
  projectId: string,
  databaseId: string,
  tableId: string,
) {
  return useQuery({
    queryKey: queryKeys.indexes(projectId, databaseId, tableId),
    queryFn: ({ signal }) =>
      fetchAllCursorPages(
        (cursor) =>
          api
            .GET(
              "/v1/projects/{projectID}/databases/{databaseID}/tables/{tableID}/indexes",
              {
                params: {
                  path: {
                    projectID: projectId,
                    databaseID: databaseId,
                    tableID: tableId,
                  },
                  query: withCursorPage({ cursor }),
                },
                signal,
              },
            )
            .then(unwrap),
        (page) => page.indexes,
        { signal },
      ),
  });
}

export function useDatabaseRow(
  projectId: string,
  databaseId: string,
  tableId: string,
  rowId: string,
) {
  return useQuery({
    queryKey: queryKeys.row(projectId, databaseId, tableId, rowId),
    enabled: Boolean(rowId),
    queryFn: cancellableQuery((signal) =>
      api.GET(
        "/v1/projects/{projectID}/databases/{databaseID}/tables/{tableID}/rows/{rowID}",
        {
          params: {
            path: {
              projectID: projectId,
              databaseID: databaseId,
              tableID: tableId,
              rowID: rowId,
            },
          },
          signal,
        },
      ),
    ),
  });
}

export function useDatabases(
  projectId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.databases(projectId ?? ""), params],
    enabled: Boolean(projectId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/databases", {
        params: { path: { projectID: projectId! }, query: params },
        signal,
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
    queryFn: ({ signal }) =>
      fetchAllCursorPages(
        (cursor) =>
          api
            .GET("/v1/projects/{projectID}/databases", {
              params: {
                path: { projectID: projectId! },
                query: withCursorPage(cursor ? { cursor } : undefined),
              },
              signal,
            })
            .then(unwrap),
        (page) => page.databases,
        { signal },
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
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/databases/{databaseID}", {
        params: { path: { projectID: projectId!, databaseID: databaseId! } },
        signal,
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
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/databases/{databaseID}/tables", {
        params: {
          path: { projectID: projectId!, databaseID: databaseId! },
          query: params,
        },
        signal,
      }),
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
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/databases/{databaseID}/backups", {
        params: {
          path: { projectID: projectId!, databaseID: databaseId! },
          query: params,
        },
        signal,
      }),
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
    queryFn: cancellableQuery((signal) =>
      api.GET(
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
          signal,
        },
      ),
    ),
    placeholderData: keepPreviousData,
  });
}
