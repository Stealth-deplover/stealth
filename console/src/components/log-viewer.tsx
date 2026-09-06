"use client";

import { Copy, Pause, Play, Search, Trash2 } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
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
  const [paused, setPaused] = useState(false);
  const [localCleared, setLocalCleared] = useState(false);
  const bottomRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!enabled || paused) return;
    let cancelled = false;
    const pull = async () => {
      try {
        const next = await fetchPage(after);
        if (cancelled || !next.length) return;
        setLines((current) => { const known = new Set(current.map((line) => line.sequence)); return [...current, ...next.filter((line) => !known.has(line.sequence))].slice(-2000); });
        setAfter((current) => Math.max(current ?? 0, ...next.map((line) => line.sequence)));
      } catch (error) {
        if (!cancelled) toast.error(error instanceof Error ? error.message : "Unable to fetch logs");
      }
    };
    void pull();
    const timer = window.setInterval(() => void pull(), 3000);
    return () => { cancelled = true; window.clearInterval(timer); };
  }, [after, enabled, fetchPage, paused]);

  useEffect(() => { if (!paused) bottomRef.current?.scrollIntoView({ behavior: "smooth" }); }, [lines, paused]);
  const visible = useMemo(() => lines.filter((line) => `${line.level} ${line.message}`.toLowerCase().includes(search.toLowerCase())), [lines, search]);
  const copy = async () => { await navigator.clipboard.writeText(lines.map((line) => `[${formatDate(line.created_at)}] ${line.level.toUpperCase()} ${line.message}`).join("\n")); toast.success("Logs copied"); };
  return <Card className="overflow-hidden"><CardHeader className="border-b border-stealth-border pb-4"><div className="flex flex-wrap items-center justify-between gap-3"><div><CardTitle>{title}</CardTitle><p className="mt-1 text-xs text-slate-500">{description} · cursor {after ?? "start"}</p></div><div className="flex items-center gap-1.5"><Badge variant={paused ? "warning" : "success"}>{paused ? "Paused" : "Following"}</Badge><Button variant="ghost" size="icon" onClick={() => setPaused((value) => !value)} aria-label={paused ? "Resume autoscroll" : "Pause autoscroll"}>{paused ? <Play className="size-3.5" /> : <Pause className="size-3.5" />}</Button><Button variant="ghost" size="icon" onClick={() => void copy()} aria-label="Copy logs"><Copy className="size-3.5" /></Button><Button variant="ghost" size="icon" onClick={() => { setLines([]); setAfter(undefined); setLocalCleared(true); }} aria-label="Clear local view"><Trash2 className="size-3.5" /></Button></div></div><div className="relative mt-3 max-w-sm"><Search className="absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-slate-600" /><Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Filter current view" className="h-8 pl-8 text-xs" /></div></CardHeader><CardContent className="p-0"><div className="scrollbar-thin max-h-[32rem] overflow-y-auto bg-[#07090c] p-4 font-mono text-xs leading-6">{visible.length ? visible.map((line) => <div key={line.sequence} className="flex gap-3"><span className="w-32 shrink-0 text-slate-700">{formatDate(line.created_at)}</span><span className={line.level === "error" ? "text-rose-300" : line.level === "warn" ? "text-amber-300" : line.level === "debug" ? "text-slate-500" : "text-cyan-200"}>{line.level.padEnd(5, " ")}</span><span className="whitespace-pre-wrap text-slate-300">{line.message}</span></div>) : <div className="py-16 text-center text-slate-600">{localCleared ? "Local view cleared. New lines will appear here." : "Waiting for log lines…"}</div>}<div ref={bottomRef} /></div></CardContent></Card>;
}
