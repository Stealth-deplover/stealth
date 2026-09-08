"use client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { queryKeys } from "@/api/query-keys";
import type { components } from "@/api/generated/schema";

export function useCreateDatabaseColumn(
  projectId: string,
  databaseId: string,
  tableId: string,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["CreateDatabaseColumnRequest"],
    ) =>
      unwrap(
        await api.POST(
          "/v1/projects/{projectID}/databases/{databaseID}/tables/{tableID}/columns",
          {
            params: {
              path: {
                projectID: projectId,
                databaseID: databaseId,
                tableID: tableId,
              },
            },
            body,
          },
        ),
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.columns(projectId, databaseId, tableId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.rows(projectId, databaseId, tableId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.rowScope(projectId, databaseId, tableId),
      });
    },
  });
}

export function useCreateDatabaseRow(
  projectId: string,
  databaseId: string,
  tableId: string,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["CreateDatabaseRowRequest"],
    ) =>
      unwrap(
        await api.POST(
          "/v1/projects/{projectID}/databases/{databaseID}/tables/{tableID}/rows",
          {
            params: {
              path: {
                projectID: projectId,
                databaseID: databaseId,
                tableID: tableId,
              },
            },
            body,
          },
        ),
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.rows(projectId, databaseId, tableId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.rowScope(projectId, databaseId, tableId),
      });
    },
  });
}

export function useUpdateDatabaseRow(
  projectId: string,
  databaseId: string,
  tableId: string,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({
      rowId,
      body,
    }: {
      rowId: string;
      body: components["schemas"]["UpdateDatabaseRowRequest"];
    }) =>
      unwrap(
        await api.PATCH(
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
            body,
          },
        ),
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.rows(projectId, databaseId, tableId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.rowScope(projectId, databaseId, tableId),
      });
    },
  });
}

export function useDeleteDatabaseRow(
  projectId: string,
  databaseId: string,
  tableId: string,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (rowId: string) =>
      unwrap(
        await api.DELETE(
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
          },
        ),
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.rows(projectId, databaseId, tableId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.rowScope(projectId, databaseId, tableId),
      });
    },
  });
}

export function useCreateDatabase(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: components["schemas"]["CreateDatabaseRequest"]) =>
      unwrap(
        await api.POST("/v1/projects/{projectID}/databases", {
          params: { path: { projectID: projectId } },
          body,
        }),
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.databases(projectId),
      }),
  });
}

export function useCreateDatabaseTable(projectId: string, databaseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["CreateDatabaseTableRequest"],
    ) =>
      unwrap(
        await api.POST(
          "/v1/projects/{projectID}/databases/{databaseID}/tables",
          {
            params: {
              path: { projectID: projectId, databaseID: databaseId },
            },
            body,
          },
        ),
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.tables(projectId, databaseId),
      }),
  });
}

export function useCreateDatabaseBackup(projectId: string, databaseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () =>
      unwrap(
        await api.POST(
          "/v1/projects/{projectID}/databases/{databaseID}/backups",
          {
            params: {
              path: { projectID: projectId, databaseID: databaseId },
              query: { max_rows: 10000 },
            },
          },
        ),
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.databaseBackups(projectId, databaseId),
      }),
  });
}

export function useDeleteDatabaseBackup(projectId: string, databaseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (backupId: string) =>
      unwrap(
        await api.DELETE(
          "/v1/projects/{projectID}/databases/{databaseID}/backups/{backupID}",
          {
            params: {
              path: {
                projectID: projectId,
                databaseID: databaseId,
                backupID: backupId,
              },
            },
          },
        ),
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.databaseBackups(projectId, databaseId),
      }),
  });
}

export function useRestoreDatabaseBackup(
  projectId: string,
  databaseId: string,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (backupId: string) =>
      unwrap(
        await api.POST(
          "/v1/projects/{projectID}/databases/{databaseID}/backups/{backupID}/restore",
          {
            params: {
              path: {
                projectID: projectId,
                databaseID: databaseId,
                backupID: backupId,
              },
            },
          },
        ),
      ),
    onSuccess: () => {
      const scopes = [
        queryKeys.rowsScope(projectId, databaseId),
        queryKeys.rowDatabaseScope(projectId, databaseId),
        queryKeys.tableScope(projectId, databaseId),
        queryKeys.columnsScope(projectId, databaseId),
        queryKeys.indexesScope(projectId, databaseId),
      ];
      queryClient.invalidateQueries({
        predicate: (query) =>
          scopes.some((scope) =>
            scope.every((value, index) => query.queryKey[index] === value),
          ),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.database(projectId, databaseId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.tables(projectId, databaseId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.databaseBackups(projectId, databaseId),
      });
    },
  });
}
