"use client";

import dynamic from "next/dynamic";
import { LoadingState } from "@/components/feedback/loading-state";
import { Skeleton } from "@/components/ui/skeleton";

const ServicesCanvasView = dynamic(
  () =>
    import("./services-canvas-view").then(
      (module) => module.ServicesCanvasView,
    ),
  {
    ssr: false,
    loading: () => (
      <LoadingState
        label="Loading services canvas…"
        className="h-[calc(100vh-15rem)] min-h-[520px] w-full"
      >
        <Skeleton className="h-full w-full rounded-2xl" />
      </LoadingState>
    ),
  },
);

export function ServicesCanvasLoader({
  organizationId,
  projectId,
}: {
  organizationId: string;
  projectId: string;
}) {
  return (
    <ServicesCanvasView organizationId={organizationId} projectId={projectId} />
  );
}
