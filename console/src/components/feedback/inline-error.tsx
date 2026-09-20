import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export function InlineError({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <p
      className={cn("text-sm text-coral-red", className)}
      role="alert"
      aria-atomic="true"
    >
      {children}
    </p>
  );
}
