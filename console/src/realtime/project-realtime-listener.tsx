"use client";

import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { apiUrl } from "@/api/client";
import {
  eventTypesForProjectStream,
  realtimeInvalidationKeys,
  type RealtimeNotification,
} from "@/realtime/invalidation";

export function ProjectRealtimeListener({
  projectId,
}: Readonly<{ projectId: string | undefined }>) {
  const queryClient = useQueryClient();

  useEffect(() => {
    if (!projectId || typeof EventSource === "undefined") return;
    const events = eventTypesForProjectStream().join(",");
    const source = new EventSource(
      apiUrl(
        `/v1/projects/${encodeURIComponent(projectId)}/realtime?events=${encodeURIComponent(events)}`,
      ),
      { withCredentials: true },
    );
    const listeners = eventTypesForProjectStream().map((eventType) => {
      const listener = (message: MessageEvent<string>) => {
        try {
          const event = JSON.parse(message.data) as RealtimeNotification;
          for (const queryKey of realtimeInvalidationKeys(projectId, event)) {
            void queryClient.invalidateQueries({ queryKey });
          }
        } catch {
          // A malformed notification cannot become client state. Existing
          // bounded polling remains the recovery path.
        }
      };
      source.addEventListener(eventType, listener);
      return { eventType, listener };
    });
    return () => {
      for (const { eventType, listener } of listeners) {
        source.removeEventListener(eventType, listener);
      }
      source.close();
    };
  }, [projectId, queryClient]);

  return null;
}
