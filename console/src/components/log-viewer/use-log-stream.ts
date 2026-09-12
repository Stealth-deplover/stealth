"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import type { LogLine, LogSource } from "./log-source";
export type { LogLine } from "./log-source";

type UseLogStreamOptions = {
  source: LogSource | null;
  enabled?: boolean;
  polling?: boolean;
};

export function useLogStream({
  source,
  enabled = true,
  polling = enabled,
}: UseLogStreamOptions) {
  const [lines, setLines] = useState<LogLine[]>([]);
  const [after, setAfter] = useState<number | undefined>();
  const [localCleared, setLocalCleared] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const afterRef = useRef<number | undefined>(undefined);
  const inFlightRef = useRef(false);
  const activeControllerRef = useRef<AbortController | null>(null);
  const sourceRef = useRef<LogSource | null>(source);
  const sourceKey = source?.key ?? null;
  const previousSourceKeyRef = useRef(sourceKey);

  useEffect(() => {
    sourceRef.current = source;
  }, [source]);

  const resetStream = useCallback(() => {
    afterRef.current = undefined;
    setAfter(undefined);
    setLines([]);
    setLocalCleared(false);
    setError(null);
    setLoading(false);
  }, []);

  const pull = useCallback(async () => {
    const currentSource = sourceRef.current;
    if (!enabled || !currentSource || inFlightRef.current) return;

    const controller = new AbortController();
    inFlightRef.current = true;
    activeControllerRef.current = controller;
    setLoading(true);
    try {
      const next = await currentSource.fetchPage(
        afterRef.current,
        controller.signal,
      );
      if (controller.signal.aborted) return;

      setError(null);
      if (!next.length) return;

      const latest = Math.max(...next.map((line) => line.sequence));
      afterRef.current = Math.max(afterRef.current ?? 0, latest);
      setAfter(afterRef.current);
      setLines((current) => {
        const known = new Set(current.map((line) => line.sequence));
        return [...current, ...next.filter((line) => !known.has(line.sequence))]
          .sort((a, b) => a.sequence - b.sequence)
          .slice(-2000);
      });
      setLocalCleared(false);
    } catch (caught) {
      if (controller.signal.aborted) return;

      setError(
        caught instanceof Error ? caught.message : "Unable to fetch logs.",
      );
    } finally {
      if (activeControllerRef.current === controller) {
        activeControllerRef.current = null;
        inFlightRef.current = false;
        setLoading(false);
      }
    }
  }, [enabled]);

  useEffect(() => {
    if (previousSourceKeyRef.current !== sourceKey) {
      previousSourceKeyRef.current = sourceKey;
      // The stream belongs to the resource identity. Reset its cursor and
      // retained lines before starting a request for the new source.
      resetStream();
    }

    if (!enabled || !sourceKey) {
      // An aborted pull does not update loading state in its own finally block.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setLoading(false);
      return;
    }

    let cancelled = false;
    let timer: number | undefined;
    const run = async () => {
      await pull();
      if (!cancelled && polling) {
        timer = window.setTimeout(() => void run(), 3_000);
      }
    };
    void run();

    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearTimeout(timer);
      activeControllerRef.current?.abort();
      activeControllerRef.current = null;
      inFlightRef.current = false;
    };
  }, [enabled, polling, pull, resetStream, sourceKey]);

  const clearLocal = useCallback(() => {
    setLines([]);
    setLocalCleared(true);
  }, []);

  return { lines, after, loading, error, localCleared, clearLocal };
}
