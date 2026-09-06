import type { ReactNode } from "react";

export function PageHeader({ eyebrow, title, description, actions }: { eyebrow?: string; title: string; description?: string; actions?: ReactNode }) {
  return <div className="mb-7 flex flex-col justify-between gap-4 sm:flex-row sm:items-end">
    <div>
      {eyebrow ? <p className="mb-2 text-[11px] font-medium uppercase tracking-[0.18em] text-cyan-300/80">{eyebrow}</p> : null}
      <h1 className="text-2xl font-semibold tracking-tight text-white sm:text-3xl">{title}</h1>
      {description ? <p className="mt-2 max-w-2xl text-sm leading-6 text-stealth-muted">{description}</p> : null}
    </div>
    {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
  </div>;
}
