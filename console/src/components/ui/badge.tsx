import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const badgeVariants = cva(
  "inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] font-medium",
  {
    variants: {
      variant: {
        default: "border-cyan-300/25 bg-cyan-300/10 text-cyan-200",
        neutral: "border-white/10 bg-white/[0.06] text-slate-300",
        success: "border-emerald-300/25 bg-emerald-300/10 text-emerald-200",
        warning: "border-amber-300/25 bg-amber-300/10 text-amber-200",
        error: "border-rose-300/25 bg-rose-300/10 text-rose-200",
        building: "border-violet-300/25 bg-violet-300/10 text-violet-200",
      },
    },
    defaultVariants: { variant: "neutral" },
  },
);

export function Badge({
  className,
  variant,
  ...props
}: React.HTMLAttributes<HTMLSpanElement> & VariantProps<typeof badgeVariants>) {
  return (
    <span className={cn(badgeVariants({ variant }), className)} {...props} />
  );
}

export function StatusBadge({ status }: { status: string | null | undefined }) {
  const normalized = status?.toLowerCase() ?? "unknown";
  const variant =
    normalized === "ready" ||
    normalized === "active" ||
    normalized === "available" ||
    normalized === "succeeded" ||
    normalized === "healthy" ||
    normalized === "completed"
      ? "success"
      : normalized === "failed" ||
          normalized === "error" ||
          normalized === "blocked"
        ? "error"
        : normalized === "building" ||
            normalized === "running" ||
            normalized === "queued" ||
            normalized === "processing"
          ? "building"
          : normalized === "warning" ||
              normalized === "past_due" ||
              normalized === "degraded"
            ? "warning"
            : "neutral";
  const label = status
    ? status
        .replace(/_/g, " ")
        .replace(/^\w/, (character) => character.toUpperCase())
    : "Unknown";
  return (
    <Badge variant={variant}>
      <span
        className={cn(
          "size-1.5 rounded-full bg-current",
          variant === "building" && "animate-pulse",
        )}
      />
      {label}
    </Badge>
  );
}
