import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { LogLine, LogSource } from "./log-source";
import { useLogStream } from "./use-log-stream";

type PendingRequest = {
  after?: number;
  signal?: AbortSignal;
  resolve: (lines: LogLine[]) => void;
};

function logLine(sequence: number): LogLine {
  return {
    sequence,
    level: "info",
    message: `line ${sequence}`,
    created_at: "2026-09-12T00:00:00Z",
  };
}

function source(key: string, fetchPage: LogSource["fetchPage"]): LogSource {
  return { key, fetchPage };
}

afterEach(() => {
  vi.useRealTimers();
});

describe("useLogStream", () => {
  it("waits for a pull to finish before scheduling the next poll", async () => {
    vi.useFakeTimers();
    const requests: PendingRequest[] = [];
    const fetchPage = vi.fn(
      (after?: number, signal?: AbortSignal) =>
        new Promise<LogLine[]>((resolve) => {
          requests.push({ after, signal, resolve });
        }),
    );
    const logSource = source("function-build:one", fetchPage);

    const { result, unmount } = renderHook(() =>
      useLogStream({ source: logSource, polling: true }),
    );

    expect(fetchPage).toHaveBeenCalledTimes(1);
    expect(requests[0].after).toBeUndefined();
    expect(requests[0].signal?.aborted).toBe(false);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(9_000);
    });
    expect(fetchPage).toHaveBeenCalledTimes(1);

    await act(async () => {
      requests[0].resolve([logLine(1)]);
      await Promise.resolve();
    });
    expect(result.current.after).toBe(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3_000);
    });
    expect(fetchPage).toHaveBeenCalledTimes(2);
    expect(requests[1].after).toBe(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3_000);
    });
    expect(fetchPage).toHaveBeenCalledTimes(2);

    await act(async () => {
      requests[1].resolve([logLine(2)]);
      await Promise.resolve();
      await vi.advanceTimersByTimeAsync(3_000);
    });
    expect(fetchPage).toHaveBeenCalledTimes(3);
    expect(requests[2].after).toBe(2);

    unmount();
  });

  it("aborts an active pull when unmounted", async () => {
    const requests: PendingRequest[] = [];
    const fetchPage = vi.fn(
      (_after?: number, signal?: AbortSignal) =>
        new Promise<LogLine[]>((resolve) => {
          requests.push({ signal, resolve });
        }),
    );
    const logSource = source("agent-run:one", fetchPage);

    const { unmount } = renderHook(() =>
      useLogStream({ source: logSource, polling: false }),
    );

    expect(requests[0].signal?.aborted).toBe(false);
    unmount();
    expect(requests[0].signal?.aborted).toBe(true);

    await act(async () => {
      requests[0].resolve([logLine(1)]);
      await Promise.resolve();
    });
  });

  it("resets the cursor and retained lines when the source changes", async () => {
    const requests: PendingRequest[] = [];
    const fetchPage = vi.fn(
      (after?: number, signal?: AbortSignal) =>
        new Promise<LogLine[]>((resolve) => {
          requests.push({ after, signal, resolve });
        }),
    );
    const firstSource = source("function-build:first", fetchPage);
    const secondSource = source("function-build:second", fetchPage);

    const { result, rerender } = renderHook(
      ({ logSource }: { logSource: LogSource }) =>
        useLogStream({ source: logSource, polling: false }),
      { initialProps: { logSource: firstSource } },
    );

    await act(async () => {
      requests[0].resolve([logLine(4)]);
      await Promise.resolve();
    });
    expect(result.current.lines).toHaveLength(1);
    expect(result.current.after).toBe(4);

    await act(async () => {
      rerender({ logSource: secondSource });
      await Promise.resolve();
    });

    expect(result.current.lines).toEqual([]);
    expect(result.current.after).toBeUndefined();
    expect(requests[1].after).toBeUndefined();
  });

  it("aborts the previous source before accepting a new source response", async () => {
    const requests: PendingRequest[] = [];
    const fetchPage = vi.fn(
      (after?: number, signal?: AbortSignal) =>
        new Promise<LogLine[]>((resolve) => {
          requests.push({ after, signal, resolve });
        }),
    );
    const firstSource = source("site-build:first", fetchPage);
    const secondSource = source("site-build:second", fetchPage);

    const { result, rerender } = renderHook(
      ({ logSource }: { logSource: LogSource }) =>
        useLogStream({ source: logSource, polling: false }),
      { initialProps: { logSource: firstSource } },
    );

    await act(async () => {
      rerender({ logSource: secondSource });
      await Promise.resolve();
    });
    expect(requests[0].signal?.aborted).toBe(true);
    expect(requests[1].after).toBeUndefined();

    await act(async () => {
      requests[0].resolve([logLine(1)]);
      requests[1].resolve([logLine(2)]);
      await Promise.resolve();
    });
    expect(result.current.lines.map((line) => line.sequence)).toEqual([2]);
  });
});
