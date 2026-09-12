"use client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap, uploadMultipart } from "@/api/client";
import { queryKeys } from "@/api/query-keys";
import { applyCacheChanges } from "@/api/cache-coherence";
import type { components } from "@/api/generated/schema";

export function useUploadStorageFile(projectId: string, bucketId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (file: File) => {
      const form = new FormData();
      form.append("file", file);
      return uploadMultipart<components["schemas"]["StorageFileResponse"]>(
        `/v1/projects/${projectId}/storage/buckets/${bucketId}/files`,
        form,
      );
    },
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.files(projectId, bucketId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.bucket(projectId, bucketId),
      });
      queryClient.invalidateQueries({ queryKey: queryKeys.buckets(projectId) });
    },
  });
}

export function useRenameStorageFile(projectId: string, bucketId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({
      fileId,
      body,
    }: {
      fileId: string;
      body: components["schemas"]["UpdateStorageFileRequest"];
    }) =>
      unwrap(
        await api.PATCH(
          "/v1/projects/{projectID}/storage/buckets/{bucketID}/files/{fileID}",
          {
            params: {
              path: {
                projectID: projectId,
                bucketID: bucketId,
                fileID: fileId,
              },
            },
            body,
          },
        ),
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.files(projectId, bucketId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.fileScope(projectId, bucketId),
      });
    },
  });
}

export function useUpdateStorageBucket(projectId: string, bucketId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["UpdateStorageBucketRequest"],
    ) =>
      unwrap(
        await api.PATCH("/v1/projects/{projectID}/storage/buckets/{bucketID}", {
          params: { path: { projectID: projectId, bucketID: bucketId } },
          body,
        }),
      ),
    onSuccess: () =>
      applyCacheChanges(queryClient, [
        {
          kind: "storage-bucket",
          projectId,
          bucketId,
          includeDetail: true,
        },
      ]),
  });
}

export function useCreateBucket(projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (
      body: components["schemas"]["CreateStorageBucketRequest"],
    ) =>
      unwrap(
        await api.POST("/v1/projects/{projectID}/storage/buckets", {
          params: { path: { projectID: projectId } },
          body,
        }),
      ),
    onSuccess: () =>
      applyCacheChanges(queryClient, [{ kind: "storage-bucket", projectId }]),
  });
}

export function useDeleteStorageFile(projectId: string, bucketId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (fileId: string) =>
      unwrap(
        await api.DELETE(
          "/v1/projects/{projectID}/storage/buckets/{bucketID}/files/{fileID}",
          {
            params: {
              path: {
                projectID: projectId,
                bucketID: bucketId,
                fileID: fileId,
              },
            },
          },
        ),
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.files(projectId, bucketId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.bucket(projectId, bucketId),
      });
      queryClient.invalidateQueries({ queryKey: queryKeys.buckets(projectId) });
    },
  });
}
