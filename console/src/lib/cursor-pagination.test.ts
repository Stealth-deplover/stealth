import { describe, expect, it } from "vitest";
import { appendCursorHistory, cursorHistoryIndex, fetchAllCursorPages, previousCursor } from "@/lib/cursor-pagination";

describe("cursor pagination history", () => {
  it("starts at the first page and records the next cursor", () => {
    const pageOne = [null] as const;
    const pageTwo = appendCursorHistory(pageOne, null, "abc");

    expect(pageTwo).toEqual([null, "abc"]);
    expect(cursorHistoryIndex(pageTwo, "abc")).toBe(1);
    expect(previousCursor(pageTwo, "abc")).toBeNull();
  });

  it("supports next then previous without inventing a cursor", () => {
    const history = appendCursorHistory(appendCursorHistory([null], null, "abc"), "abc", "def");

    expect(history).toEqual([null, "abc", "def"]);
    expect(previousCursor(history, "def")).toBe("abc");
    expect(previousCursor(history, null)).toBeUndefined();
  });

  it("does not add a missing next cursor", () => {
    expect(appendCursorHistory([null, "abc"], "abc", "abc")).toEqual([null, "abc"]);
  });

  it("follows server cursors until the API is exhausted", async () => {
    const requests: (string | undefined)[] = [];
    const pages = new Map<string | undefined, { items: string[]; pagination: { next_cursor: string | null } }>([
      [undefined, { items: ["one"], pagination: { next_cursor: "abc" } }],
      ["abc", { items: ["two"], pagination: { next_cursor: null } }],
    ]);

    const items = await fetchAllCursorPages(
      async (cursor) => {
        requests.push(cursor);
        return pages.get(cursor);
      },
      (page) => page.items,
    );

    expect(requests).toEqual([undefined, "abc"]);
    expect(items).toEqual(["one", "two"]);
  });

  it("fails closed when the API repeats a cursor", async () => {
    await expect(fetchAllCursorPages(
      async () => ({ items: ["one"], pagination: { next_cursor: "same" } }),
      (page) => page.items,
    )).rejects.toThrow("repeated pagination cursor");
  });
});
