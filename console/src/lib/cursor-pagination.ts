export type Cursor = string | null;

export type CursorPage = { pagination: { next_cursor: string | null } };

export async function fetchAllCursorPages<TPage extends CursorPage, TItem>(fetchPage: (cursor?: string) => Promise<TPage | undefined>, getItems: (page: TPage) => TItem[]) {
  const items: TItem[] = [];
  const seenCursors = new Set<string>();
  let cursor: string | undefined;

  while (true) {
    const page = await fetchPage(cursor);
    if (!page) return items;
    items.push(...getItems(page));
    cursor = page.pagination.next_cursor ?? undefined;
    if (!cursor) return items;
    if (seenCursors.has(cursor)) throw new Error("The API returned a repeated pagination cursor.");
    seenCursors.add(cursor);
  }
}

export function appendCursorHistory(history: readonly Cursor[], current: Cursor, next: string): Cursor[] {
  const currentIndex = history.indexOf(current);
  const prefix = currentIndex >= 0 ? history.slice(0, currentIndex + 1) : [...history, current];
  return prefix.at(-1) === next ? [...prefix] : [...prefix, next];
}

export function cursorHistoryIndex(history: readonly Cursor[], current: Cursor): number {
  return history.indexOf(current);
}

export function previousCursor(history: readonly Cursor[], current: Cursor): Cursor | undefined {
  const index = cursorHistoryIndex(history, current);
  return index > 0 ? history[index - 1] : undefined;
}
