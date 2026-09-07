"use client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { queryKeys } from "@/api/query-keys";
import type { components } from "@/api/generated/schema";

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
      queryClient.invalidateQueries({ queryKey: queryKeys.buckets(projectId) }),
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
