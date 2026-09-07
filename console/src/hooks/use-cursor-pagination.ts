"use client";

import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useState } from "react";
import { appendCursorHistory, cursorContextKey, cursorHistoryIndex, previousCursor, updateCursorQuery, type Cursor } from "@/lib/cursor-pagination";

export function useCursorPagination(param = "cursor") {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const currentCursor: Cursor = searchParams.get(param) || null;
  const contextKey = `${pathname}:${cursorContextKey(searchParams.toString(), param)}`;
  const [paginationState, setPaginationState] = useState<{ contextKey: string; history: readonly Cursor[] }>({ contextKey, history: [currentCursor] });
  // A changed filter/sort/query context gets a fresh effective history. The
  // state is replaced on the next pagination action, avoiding an effect-driven
  // render while still preventing a stale Previous cursor from being exposed.
  const history = paginationState.contextKey === contextKey ? paginationState.history : [currentCursor];

  const currentIndex = cursorHistoryIndex(history, currentCursor);

  const navigate = (cursor: Cursor) => {
    const next = updateCursorQuery(searchParams.toString(), param, cursor);
    router.replace(`${pathname}${next ? `?${next}` : ""}`, { scroll: false });
  };

  const goNext = (next: string | null | undefined) => {
    if (!next || next === currentCursor) return;
    setPaginationState((current) => {
      const currentHistory = current.contextKey === contextKey ? current.history : [currentCursor];
      return { contextKey, history: appendCursorHistory(currentHistory, currentCursor, next) };
    });
    navigate(next);
  };

  const goPrevious = () => {
    const cursor = previousCursor(history, currentCursor);
    if (cursor !== undefined) navigate(cursor);
  };

  const goFirst = () => {
    if (currentCursor === null) return;
    setPaginationState({ contextKey, history: [null] });
    navigate(null);
  };

  return {
    cursor: currentCursor ?? undefined,
    canFirst: currentCursor !== null,
    canPrevious: currentIndex > 0,
    goFirst,
    goNext,
    goPrevious,
  };
}
