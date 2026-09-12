import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useLogStream, type LogLine } from "./use-log-stream";

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

    const { result, unmount } = renderHook(() =>
      useLogStream({ fetchPage, polling: true }),
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

    const { unmount } = renderHook(() =>
      useLogStream({ fetchPage, polling: false }),
    );

    expect(requests[0].signal?.aborted).toBe(false);
    unmount();
    expect(requests[0].signal?.aborted).toBe(true);

    await act(async () => {
      requests[0].resolve([logLine(1)]);
      await Promise.resolve();
    });
  });
});
