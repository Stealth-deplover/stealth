import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { LogLine, LogPage, LogSource } from "./log-source";
import { useLogStream } from "./use-log-stream";

type PendingRequest = {
  cursor?: string;
  signal?: AbortSignal;
  resolve: (page: LogPage) => void;
};

function line(id: string, message = id): LogLine {
  return { id, level: "info", message, created_at: "2026-09-12T00:00:00Z" };
}

function page(lines: LogLine[], nextCursor?: string): LogPage {
  return { lines, nextCursor };
}

function source(key: string, fetchPage: LogSource["fetchPage"]): LogSource {
  return { key, fetchPage };
}

afterEach(() => vi.useRealTimers());

describe("useLogStream", () => {
  it("waits for a page before polling and advances opaque cursors", async () => {
    vi.useFakeTimers();
    const requests: PendingRequest[] = [];
    const fetchPage = vi.fn(
      (cursor?: string, signal?: AbortSignal) =>
        new Promise<LogPage>((resolve) => {
          requests.push({ cursor, signal, resolve });
        }),
    );
    const { result, unmount } = renderHook(() =>
      useLogStream({ source: source("runtime", fetchPage), polling: true }),
    );
    expect(requests).toHaveLength(1);
    expect(requests[0].cursor).toBeUndefined();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(9000);
    });
    expect(fetchPage).toHaveBeenCalledTimes(1);
    await act(async () => {
      requests[0].resolve(page([line("stable-1")], "opaque-cursor-1"));
      await Promise.resolve();
    });
    expect(result.current.cursor).toBe("opaque-cursor-1");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(requests[1].cursor).toBe("opaque-cursor-1");
    await act(async () => {
      requests[1].resolve(page([], "opaque-cursor-1"));
      await Promise.resolve();
    });
    unmount();
  });

  it("cancels a pending request when the viewer unmounts", async () => {
    const requests: PendingRequest[] = [];
    const fetchPage = vi.fn(
      (_cursor?: string, signal?: AbortSignal) =>
        new Promise<LogPage>((resolve) => {
          requests.push({ signal, resolve });
        }),
    );
    const { unmount } = renderHook(() =>
      useLogStream({ source: source("one", fetchPage), polling: false }),
    );
    expect(requests[0].signal?.aborted).toBe(false);
    unmount();
    expect(requests[0].signal?.aborted).toBe(true);
    await act(async () => {
      requests[0].resolve(page([line("ignored")]));
      await Promise.resolve();
    });
  });

  it("resets source state, cancels the old source, and accepts the new source only", async () => {
    const requests: PendingRequest[] = [];
    const fetchPage = vi.fn(
      (cursor?: string, signal?: AbortSignal) =>
        new Promise<LogPage>((resolve) => {
          requests.push({ cursor, signal, resolve });
        }),
    );
    const first = source("first", fetchPage);
    const second = source("second", fetchPage);
    const { result, rerender } = renderHook(
      ({ current, polling }: { current: LogSource; polling: boolean }) =>
        useLogStream({ source: current, polling }),
      {
        initialProps: { current: first, polling: false },
      },
    );
    await act(async () => {
      requests[0].resolve(page([line("old")], "old-cursor"));
      await Promise.resolve();
    });
    expect(result.current.lines.map((item) => item.id)).toEqual(["old"]);
    await act(async () => {
      rerender({ current: first, polling: true });
      await Promise.resolve();
    });
    expect(requests[1].cursor).toBe("old-cursor");
    await act(async () => {
      rerender({ current: second, polling: false });
      await Promise.resolve();
    });
    expect(requests[1].signal?.aborted).toBe(true);
    expect(requests[2].cursor).toBeUndefined();
    await act(async () => {
      requests[1].resolve(page([line("stale")], "stale-cursor"));
      requests[2].resolve(page([line("new")], "new-cursor"));
      await Promise.resolve();
    });
    expect(result.current.lines.map((item) => item.id)).toEqual(["new"]);
    expect(result.current.cursor).toBe("new-cursor");
  });

  it("deduplicates overlapping pages and preserves source order for equal timestamps", async () => {
    const fetchPage = vi
      .fn()
      .mockResolvedValueOnce(page([line("one"), line("two")], "opaque-1"))
      .mockResolvedValueOnce(
        page([line("two"), line("three"), line("three")], "opaque-2"),
      );
    const { result, rerender } = renderHook(
      ({ polling }: { polling: boolean }) =>
        useLogStream({
          source: source("equal-time", fetchPage),
          polling,
        }),
      { initialProps: { polling: false } },
    );
    await act(async () => {
      await Promise.resolve();
    });
    expect(result.current.lines.map((item) => item.id)).toEqual(["one", "two"]);
    rerender({ polling: true });
    await act(async () => {
      await Promise.resolve();
    });
    expect(fetchPage).toHaveBeenLastCalledWith(
      "opaque-1",
      expect.any(AbortSignal),
    );
    expect(result.current.lines.map((item) => item.id)).toEqual([
      "one",
      "two",
      "three",
    ]);
  });

  it("clears only the local view and keeps the opaque cursor", async () => {
    const fetchPage = vi
      .fn()
      .mockResolvedValue(page([line("one")], "opaque-1"));
    const { result } = renderHook(() =>
      useLogStream({ source: source("clear", fetchPage), polling: false }),
    );
    await act(async () => {
      await Promise.resolve();
    });
    act(() => result.current.clearLocal());
    expect(result.current.lines).toEqual([]);
    expect(result.current.localCleared).toBe(true);
    expect(result.current.cursor).toBe("opaque-1");
  });
});
