"use client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { applyCacheChanges } from "@/api/cache-coherence";
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
    onSuccess: () =>
      applyCacheChanges(queryClient, [
        { kind: "database-table-schema", projectId, databaseId, tableId },
      ]),
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
    onSuccess: () =>
      applyCacheChanges(queryClient, [
        { kind: "database-table-rows", projectId, databaseId, tableId },
      ]),
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
    onSuccess: () =>
      applyCacheChanges(queryClient, [
        { kind: "database-table-rows", projectId, databaseId, tableId },
      ]),
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
    onSuccess: () =>
      applyCacheChanges(queryClient, [
        { kind: "database-table-rows", projectId, databaseId, tableId },
      ]),
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
      applyCacheChanges(queryClient, [{ kind: "database", projectId }]),
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
      applyCacheChanges(queryClient, [
        { kind: "database-table", projectId, databaseId },
      ]),
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
      applyCacheChanges(queryClient, [
        { kind: "database-backup", projectId, databaseId },
      ]),
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
      applyCacheChanges(queryClient, [
        { kind: "database-backup", projectId, databaseId },
      ]),
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
    onSuccess: () =>
      applyCacheChanges(queryClient, [
        { kind: "database-backup", projectId, databaseId, restore: true },
      ]),
  });
}
