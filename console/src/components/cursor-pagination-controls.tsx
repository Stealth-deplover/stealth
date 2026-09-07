"use client";

export type CursorPaginationControlsProps = {
  canFirst: boolean;
  canPrevious: boolean;
  canNext: boolean;
  onFirst: () => void;
  onPrevious: () => void;
  onNext: () => void;
  isFetching?: boolean;
  label?: string;
};

export function CursorPaginationControls({ canFirst, canPrevious, canNext, onFirst, onPrevious, onNext, isFetching, label = "Server page" }: CursorPaginationControlsProps) {
  return <div className="flex flex-wrap items-center justify-between gap-3 border-t border-stealth-border px-4 py-3 text-xs text-slate-500">
    <span>{label}{isFetching ? " · Updating…" : ""}</span>
    <div className="flex items-center gap-2">
      <button type="button" className="rounded-md border border-stealth-border px-2.5 py-1.5 transition hover:bg-white/[0.05] disabled:cursor-not-allowed disabled:opacity-40" disabled={!canFirst || isFetching} onClick={onFirst}>First</button>
      <button type="button" className="rounded-md border border-stealth-border px-2.5 py-1.5 transition hover:bg-white/[0.05] disabled:cursor-not-allowed disabled:opacity-40" disabled={!canPrevious || isFetching} onClick={onPrevious}>Previous</button>
      <button type="button" className="rounded-md border border-stealth-border px-2.5 py-1.5 transition hover:bg-white/[0.05] disabled:cursor-not-allowed disabled:opacity-40" disabled={!canNext || isFetching} onClick={onNext}>Next</button>
    </div>
  </div>;
}
