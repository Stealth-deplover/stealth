import { Skeleton } from "@/components/ui/skeleton";
import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export function LoadingState({
  rows = 4,
  label = "Loading…",
  children,
  className,
}: {
  rows?: number;
  label?: string;
  children?: ReactNode;
  className?: string;
}) {
  const accessibleLabel = label.endsWith("…") ? label.slice(0, -1) : label;

  return (
    <div
      className={cn("space-y-3", className)}
      role="status"
      aria-live="polite"
      aria-atomic="true"
      aria-label={accessibleLabel}
      aria-busy="true"
    >
      <span className="sr-only">{label}</span>
      {children ??
        Array.from({ length: rows }, (_, index) => (
          <Skeleton key={index} className="h-14 w-full" />
        ))}
    </div>
  );
}
