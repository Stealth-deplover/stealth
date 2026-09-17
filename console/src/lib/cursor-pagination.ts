export type Cursor = string | null;

export type CursorPage = { pagination: { next_cursor: string | null } };

export type CursorTraversalOptions = {
  /** A last-resort guard for a broken API that never reaches the end cursor. */
  maxPages?: number;
  /** Cancels a multi-page traversal when its owning query is cancelled. */
  signal?: AbortSignal;
};

const DEFAULT_MAX_PAGES = 1_000;

function getMaxPages(options?: CursorTraversalOptions) {
  const maxPages = options?.maxPages ?? DEFAULT_MAX_PAGES;
  if (!Number.isInteger(maxPages) || maxPages < 1) {
    throw new Error("Cursor pagination maxPages must be a positive integer.");
  }
  return maxPages;
}

async function* walkCursorPages<TPage extends CursorPage>(
  fetchPage: (cursor?: string) => Promise<TPage | undefined>,
  options?: CursorTraversalOptions,
) {
  const maxPages = getMaxPages(options);
  const seenCursors = new Set<string>();
  let cursor: string | undefined;
  let pageCount = 0;

  while (true) {
    options?.signal?.throwIfAborted();
    if (pageCount >= maxPages) {
      throw new Error(
        `Cursor pagination exceeded the maximum of ${maxPages} pages.`,
      );
    }
    if (cursor) {
      if (seenCursors.has(cursor)) {
        throw new Error("Repeated pagination cursor received from API.");
      }
      seenCursors.add(cursor);
    }

    pageCount += 1;
    const page = await fetchPage(cursor);
    if (!page) return;
    yield page;

    cursor = page.pagination.next_cursor ?? undefined;
    if (!cursor) return;
  }
}

export async function fetchAllCursorPages<TPage extends CursorPage, TItem>(
  fetchPage: (cursor?: string) => Promise<TPage | undefined>,
  getItems: (page: TPage) => TItem[],
  options?: CursorTraversalOptions,
) {
  const items: TItem[] = [];
  for await (const page of walkCursorPages(fetchPage, options)) {
    items.push(...getItems(page));
  }
  return items;
}

export async function findCursorItem<TPage extends CursorPage, TItem>(
  fetchPage: (cursor?: string) => Promise<TPage | undefined>,
  getItems: (page: TPage) => TItem[],
  predicate: (item: TItem) => boolean,
  options?: CursorTraversalOptions,
) {
  for await (const page of walkCursorPages(fetchPage, options)) {
    const item = getItems(page).find(predicate);
    if (item) return item;
  }
  return undefined;
}

export function appendCursorHistory(
  history: readonly Cursor[],
  current: Cursor,
  next: string,
): Cursor[] {
  const currentIndex = history.indexOf(current);
  const prefix =
    currentIndex >= 0
      ? history.slice(0, currentIndex + 1)
      : [...history, current];
  return prefix.at(-1) === next ? [...prefix] : [...prefix, next];
}

export function cursorHistoryIndex(
  history: readonly Cursor[],
  current: Cursor,
): number {
  return history.indexOf(current);
}

export function previousCursor(
  history: readonly Cursor[],
  current: Cursor,
): Cursor | undefined {
  const index = cursorHistoryIndex(history, current);
  return index > 0 ? history[index - 1] : undefined;
}

export function updateCursorQuery(
  search: string,
  param: string,
  cursor: Cursor,
): string {
  const next = new URLSearchParams(search);
  if (cursor) next.set(param, cursor);
  else next.delete(param);
  return next.toString();
}

export function cursorContextKey(search: string, param: string): string {
  const context = new URLSearchParams(search);
  context.delete(param);
  return context.toString();
}
