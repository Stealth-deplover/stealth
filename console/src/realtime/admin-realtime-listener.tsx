"use client";

import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { apiUrl } from "@/api/client";
import {
  adminRealtimeInvalidationKeys,
  type RealtimeNotification,
} from "@/realtime/invalidation";

export function AdminRealtimeListener() {
  const queryClient = useQueryClient();

  useEffect(() => {
    if (typeof EventSource === "undefined") return;
    const source = new EventSource(apiUrl("/v1/admin/realtime"), {
      withCredentials: true,
    });
    const listener = (message: MessageEvent<string>) => {
      try {
        const event = JSON.parse(message.data) as RealtimeNotification;
        const keys = adminRealtimeInvalidationKeys(event);
        void Promise.all(
          keys.map((queryKey) => queryClient.invalidateQueries({ queryKey })),
        );
      } catch {
        // The next canonical refetch remains the recovery path for malformed
        // notifications; no event payload is used as application state.
      }
    };
    source.addEventListener("admin", listener);
    return () => {
      source.removeEventListener("admin", listener);
      source.close();
    };
  }, [queryClient]);

  return null;
}
