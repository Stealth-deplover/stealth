import type { ReactNode } from "react";

export function AppDetailMetric({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <div className="min-w-0 space-y-1">
      <p className="text-[10px] font-medium uppercase tracking-[0.12em] text-fog">
        {label}
      </p>
      <div className="text-sm text-mist">{children}</div>
    </div>
  );
}
