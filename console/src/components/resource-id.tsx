"use client";

import { CopyButton } from "@/components/copy-button";

function shortenResourceId(value: string) {
  if (value.length <= 16) return value;
  return `${value.slice(0, 8)}…${value.slice(-4)}`;
}

export function ResourceId({
  id,
  label = "Resource ID",
}: {
  id: string;
  label?: string;
}) {
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5 text-xs text-slate-600">
      <span className="sr-only">{label}</span>
      <span
        className="max-w-[14rem] truncate font-mono text-[11px] text-slate-500"
        title={id}
      >
        {shortenResourceId(id)}
      </span>
      <CopyButton
        value={id}
        label={`Copy ${label.toLowerCase()}`}
        className="size-6 text-slate-600 hover:text-slate-200"
      />
    </span>
  );
}
