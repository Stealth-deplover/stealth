"use client";
import { useCallback, useEffect, useRef, useState } from "react";

export type LogLine = {
  sequence: number;
  level: string;
  message: string;
  created_at: string;
};

type UseLogStreamOptions = {
  fetchPage: (after?: number) => Promise<LogLine[]>;
  enabled?: boolean;
  polling?: boolean;
};

export function useLogStream({
  fetchPage,
  enabled = true,
  polling = enabled,
}: UseLogStreamOptions) {
  const [lines, setLines] = useState<LogLine[]>([]);
  const [after, setAfter] = useState<number | undefined>();
  const [localCleared, setLocalCleared] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const afterRef = useRef<number | undefined>(undefined);

  const pull = useCallback(async () => {
    if (!enabled) return;

    setLoading(true);
    try {
      const next = await fetchPage(afterRef.current);
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
      setError(
        caught instanceof Error ? caught.message : "Unable to fetch logs.",
      );
    } finally {
      setLoading(false);
    }
  }, [enabled, fetchPage]);

  useEffect(() => {
    if (!enabled) return;

    // The initial pull starts an external request; its async completion updates the viewer state.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void pull();
    if (!polling) return;
    const timer = window.setInterval(() => void pull(), 3_000);
    return () => window.clearInterval(timer);
  }, [enabled, polling, pull]);

  const clearLocal = useCallback(() => {
    setLines([]);
    setLocalCleared(true);
  }, []);

  return { lines, after, loading, error, localCleared, clearLocal };
}
