"use client";

import dynamic from "next/dynamic";
import { Skeleton } from "@/components/ui/skeleton";

const ServicesCanvasView = dynamic(() => import("./services-canvas-view").then((module) => module.ServicesCanvasView), {
  ssr: false,
  loading: () => <Skeleton className="h-[calc(100vh-15rem)] min-h-[520px] w-full rounded-2xl" />,
});

export function ServicesCanvasLoader({ organizationId, projectId }: { organizationId: string; projectId: string }) {
  return <ServicesCanvasView organizationId={organizationId} projectId={projectId} />;
}
