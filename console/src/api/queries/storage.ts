"use client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, cancellableQuery, unwrap } from "@/api/client";
import { type CursorQuery, withCursorPage } from "@/api/pagination";
import { queryKeys } from "@/api/query-keys";
import { fetchAllCursorPages } from "@/lib/cursor-pagination";

export function useStorageFile(
  projectId: string,
  bucketId: string,
  fileId: string,
) {
  return useQuery({
    queryKey: queryKeys.file(projectId, bucketId, fileId),
    enabled: Boolean(fileId),
    queryFn: cancellableQuery((signal) =>
      api.GET(
        "/v1/projects/{projectID}/storage/buckets/{bucketID}/files/{fileID}",
        {
          params: {
            path: {
              projectID: projectId,
              bucketID: bucketId,
              fileID: fileId,
            },
          },
          signal,
        },
      ),
    ),
  });
}

export function useStorageBuckets(
  projectId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.buckets(projectId ?? ""), params],
    enabled: Boolean(projectId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/storage/buckets", {
        params: { path: { projectID: projectId! }, query: params },
        signal,
      }),
    ),
    placeholderData: keepPreviousData,
  });
}

export function useCanvasStorageBuckets(projectId: string | undefined) {
  return useQuery({
    queryKey: [...queryKeys.buckets(projectId ?? ""), "canvas"],
    enabled: Boolean(projectId),
    staleTime: 30_000,
    queryFn: ({ signal }) =>
      fetchAllCursorPages(
        (cursor) =>
          api
            .GET("/v1/projects/{projectID}/storage/buckets", {
              params: {
                path: { projectID: projectId! },
                query: withCursorPage(cursor ? { cursor } : undefined),
              },
              signal,
            })
            .then(unwrap),
        (page) => page.buckets,
        { signal },
      ),
  });
}

export function useStorageBucket(
  projectId: string | undefined,
  bucketId: string | undefined,
) {
  return useQuery({
    queryKey: queryKeys.bucket(projectId ?? "", bucketId ?? ""),
    enabled: Boolean(projectId && bucketId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/storage/buckets/{bucketID}", {
        params: { path: { projectID: projectId!, bucketID: bucketId! } },
        signal,
      }),
    ),
  });
}

export function useStorageFiles(
  projectId: string | undefined,
  bucketId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.files(projectId ?? "", bucketId ?? ""), params],
    enabled: Boolean(projectId && bucketId),
    queryFn: cancellableQuery((signal) =>
      api.GET("/v1/projects/{projectID}/storage/buckets/{bucketID}/files", {
        params: {
          path: { projectID: projectId!, bucketID: bucketId! },
          query: params,
        },
        signal,
      }),
    ),
    placeholderData: keepPreviousData,
  });
}
