"use client";

import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useState } from "react";
import { appendCursorHistory, cursorHistoryIndex, previousCursor, type Cursor } from "@/lib/cursor-pagination";

export function useCursorPagination(param = "cursor") {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const currentCursor = searchParams.get(param) as Cursor;
  const [history, setHistory] = useState<readonly Cursor[]>([currentCursor]);
  const currentIndex = cursorHistoryIndex(history, currentCursor);

  const navigate = (cursor: Cursor) => {
    const next = new URLSearchParams(searchParams.toString());
    if (cursor) next.set(param, cursor);
    else next.delete(param);
    router.replace(`${pathname}${next.toString() ? `?${next.toString()}` : ""}`, { scroll: false });
  };

  const goNext = (next: string | null | undefined) => {
    if (!next) return;
    setHistory((current) => appendCursorHistory(current, currentCursor, next));
    navigate(next);
  };

  const goPrevious = () => {
    const cursor = previousCursor(history, currentCursor);
    if (cursor !== undefined) navigate(cursor);
  };

  return {
    cursor: currentCursor ?? undefined,
    canPrevious: currentIndex > 0,
    goNext,
    goPrevious,
  };
}
