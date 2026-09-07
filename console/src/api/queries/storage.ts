"use client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { type CursorQuery, withCursorPage } from "@/api/pagination";
import { queryKeys } from "@/api/query-keys";
import { fetchAllCursorPages } from "@/lib/cursor-pagination";

export function useStorageBuckets(
  projectId: string | undefined,
  query?: CursorQuery,
) {
  const params = withCursorPage(query);
  return useQuery({
    queryKey: [...queryKeys.buckets(projectId ?? ""), params],
    enabled: Boolean(projectId),
    queryFn: async () =>
      unwrap(
        await api.GET("/v1/projects/{projectID}/storage/buckets", {
          params: { path: { projectID: projectId! }, query: params },
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
    queryFn: () =>
      fetchAllCursorPages(
        (cursor) =>
          api
            .GET("/v1/projects/{projectID}/storage/buckets", {
              params: {
                path: { projectID: projectId! },
                query: withCursorPage(cursor ? { cursor } : undefined),
              },
            })
            .then(unwrap),
        (page) => page.buckets,
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
    queryFn: async () =>
      unwrap(
        await api.GET("/v1/projects/{projectID}/storage/buckets/{bucketID}", {
          params: { path: { projectID: projectId!, bucketID: bucketId! } },
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
    queryFn: async () =>
      unwrap(
        await api.GET(
          "/v1/projects/{projectID}/storage/buckets/{bucketID}/files",
          {
            params: {
              path: { projectID: projectId!, bucketID: bucketId! },
              query: params,
            },
          },
        ),
      ),
    placeholderData: keepPreviousData,
  });
}
