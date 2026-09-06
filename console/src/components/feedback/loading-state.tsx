import { Skeleton } from "@/components/ui/skeleton";

export function LoadingState({ rows = 4 }: { rows?: number }) {
  return <div className="space-y-3" aria-label="Loading">
    {Array.from({ length: rows }, (_, index) => <Skeleton key={index} className="h-14 w-full" />)}
  </div>;
}
