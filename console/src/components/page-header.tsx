import type { ReactNode } from "react";

export function PageHeader({
  eyebrow,
  title,
  description,
  actions,
}: {
  eyebrow?: string;
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <div className="mb-8 flex flex-col justify-between gap-6 sm:flex-row sm:items-end">
      <div className="min-w-0">
        {eyebrow ? (
          <p className="mb-2 text-[11px] font-medium uppercase tracking-[0.12em] text-fog">
            {eyebrow}
          </p>
        ) : null}
        <h1 className="text-[1.875rem] font-semibold leading-tight tracking-[-0.022em] text-paper sm:text-[2rem]">
          {title}
        </h1>
        {description ? (
          <p className="mt-2 max-w-2xl text-[15px] leading-6 tracking-[-0.011em] text-fog">
            {description}
          </p>
        ) : null}
      </div>
      {actions ? (
        <div className="flex shrink-0 flex-wrap items-center gap-2">
          {actions}
        </div>
      ) : null}
    </div>
  );
}
