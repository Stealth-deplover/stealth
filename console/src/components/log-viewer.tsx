"use client";

import { ArrowDown, Copy, Pause, Play, Search, Trash2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { formatDate } from "@/lib/format";

export type LogLine = { sequence: number; level: string; message: string; created_at: string };

export function LogViewer({ title = "Logs", description = "Incremental log stream", fetchPage, enabled = true }: { title?: string; description?: string; fetchPage: (after?: number) => Promise<LogLine[]>; enabled?: boolean }) {
  const [lines, setLines] = useState<LogLine[]>([]);
  const [after, setAfter] = useState<number | undefined>();
  const [search, setSearch] = useState("");
  const [autoFollow, setAutoFollow] = useState(true);
  const [localCleared, setLocalCleared] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const afterRef = useRef<number | undefined>(undefined);
  const bottomRef = useRef<HTMLDivElement>(null);

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
        return [...current, ...next.filter((line) => !known.has(line.sequence))].sort((a, b) => a.sequence - b.sequence).slice(-2000);
      });
      setLocalCleared(false);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Unable to fetch logs.");
    } finally {
      setLoading(false);
    }
  }, [enabled, fetchPage]);

  useEffect(() => {
    if (!enabled) return;
    // The initial pull starts an external request; its async completion updates the viewer state.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void pull();
    const timer = window.setInterval(() => void pull(), 3_000);
    return () => window.clearInterval(timer);
  }, [enabled, pull]);

  useEffect(() => {
    if (autoFollow) bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [autoFollow, lines]);

  const visible = useMemo(() => lines.filter((line) => `${line.level} ${line.message}`.toLowerCase().includes(search.toLowerCase())), [lines, search]);
  const copy = async () => {
    if (!lines.length) return;
    await navigator.clipboard.writeText(lines.map((line) => `[${formatDate(line.created_at)}] ${line.level.toUpperCase()} ${line.message}`).join("\n"));
  };
  const jumpToLatest = () => { setAutoFollow(true); bottomRef.current?.scrollIntoView({ behavior: "smooth" }); };

  return <Card className="overflow-hidden"><CardHeader className="border-b border-stealth-border pb-4"><div className="flex flex-wrap items-center justify-between gap-3"><div><CardTitle>{title}</CardTitle><p className="mt-1 text-xs text-slate-500">{description} · cursor {after ?? "start"}</p></div><div className="flex items-center gap-1.5"><Badge variant={autoFollow ? "success" : "warning"}>{autoFollow ? "Following" : "Autoscroll paused"}</Badge><Button variant="ghost" size="icon" onClick={() => setAutoFollow((value) => !value)} aria-label={autoFollow ? "Pause autoscroll" : "Resume autoscroll"}>{autoFollow ? <Pause className="size-3.5" /> : <Play className="size-3.5" />}</Button><Button variant="ghost" size="icon" onClick={jumpToLatest} aria-label="Jump to latest logs"><ArrowDown className="size-3.5" /></Button><Button variant="ghost" size="icon" onClick={() => void copy()} aria-label="Copy logs"><Copy className="size-3.5" /></Button><Button variant="ghost" size="icon" onClick={() => { setLines([]); setLocalCleared(true); }} aria-label="Clear local view"><Trash2 className="size-3.5" /></Button></div></div><div className="relative mt-3 max-w-sm"><Search className="absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-slate-600" /><Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Filter current view" className="h-8 pl-8 text-xs" /></div></CardHeader><CardContent className="p-0"><div className="scrollbar-thin max-h-[32rem] overflow-y-auto bg-[#07090c] p-4 font-mono text-xs leading-6">{error ? <div className="mb-3 rounded-lg border border-rose-300/20 bg-rose-400/10 px-3 py-2 font-sans text-xs text-rose-200">{error}</div> : null}{visible.length ? visible.map((line) => <div key={line.sequence} className="flex gap-3"><span className="w-32 shrink-0 text-slate-700">{formatDate(line.created_at)}</span><span className={line.level === "error" ? "text-rose-300" : line.level === "warn" ? "text-amber-300" : line.level === "debug" ? "text-slate-500" : "text-cyan-200"}>{line.level.padEnd(5, " ")}</span><span className="whitespace-pre-wrap text-slate-300">{line.message}</span></div>) : <div className="py-16 text-center font-sans text-sm text-slate-600">{loading ? "Loading log lines…" : localCleared ? "Local view cleared. New lines will appear here." : "No log lines returned yet."}</div>}<div ref={bottomRef} /></div></CardContent></Card>;
}
