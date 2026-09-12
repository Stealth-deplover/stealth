"use client";
import {
  ArrowDown,
  Check,
  Copy,
  Pause,
  Play,
  Search,
  Trash2,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { formatDate } from "@/lib/format";
import { useLogStream, type LogLine } from "./use-log-stream";

const LOG_LEVEL_CLASSES: Record<string, string> = {
  error: "text-rose-300",
  warn: "text-amber-300",
  debug: "text-slate-500",
  info: "text-cyan-200",
};

function getLogLevelClass(level: string) {
  return LOG_LEVEL_CLASSES[level] ?? LOG_LEVEL_CLASSES.info;
}

function LogToolbar({
  title,
  description,
  after,
  autoFollow,
  copied,
  search,
  onSearchChange,
  onToggleFollow,
  onJumpToLatest,
  onCopy,
  onClear,
}: {
  title: string;
  description: string;
  after?: number;
  autoFollow: boolean;
  copied: boolean;
  search: string;
  onSearchChange: (value: string) => void;
  onToggleFollow: () => void;
  onJumpToLatest: () => void;
  onCopy: () => void;
  onClear: () => void;
}) {
  return (
    <CardHeader className="border-b border-stealth-border bg-stealth-panel/95 pb-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <CardTitle>{title}</CardTitle>
          <p className="mt-1 text-xs text-slate-500">
            {description} · cursor {after ?? "start"}
          </p>
        </div>
        <div className="flex items-center gap-1.5">
          <Badge variant={autoFollow ? "success" : "warning"}>
            {autoFollow ? "Following" : "Autoscroll paused"}
          </Badge>
          <Button
            variant="ghost"
            size="icon"
            onClick={onToggleFollow}
            aria-label={autoFollow ? "Pause autoscroll" : "Resume autoscroll"}
          >
            {autoFollow ? (
              <Pause className="size-3.5" />
            ) : (
              <Play className="size-3.5" />
            )}
          </Button>
          <Button
            variant="ghost"
            size="icon"
            onClick={onJumpToLatest}
            aria-label="Jump to latest logs"
          >
            <ArrowDown className="size-3.5" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            onClick={onCopy}
            aria-label="Copy logs"
            title={copied ? "Copied" : "Copy logs"}
          >
            {copied ? (
              <Check className="size-3.5 text-emerald-300" />
            ) : (
              <Copy className="size-3.5" />
            )}
          </Button>
          <Button
            variant="ghost"
            size="icon"
            onClick={onClear}
            aria-label="Clear local view"
          >
            <Trash2 className="size-3.5" />
          </Button>
        </div>
      </div>
      <div className="relative mt-3 max-w-sm">
        <Search className="absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-slate-600" />
        <Input
          value={search}
          onChange={(event) => onSearchChange(event.target.value)}
          placeholder="Filter current view"
          className="h-8 pl-8 text-xs"
        />
      </div>
    </CardHeader>
  );
}

function LogBody({
  lines,
  error,
  loading,
  localCleared,
  emptyMessage,
  bottomRef,
}: {
  lines: LogLine[];
  error: string | null;
  loading: boolean;
  localCleared: boolean;
  emptyMessage: string;
  bottomRef: React.RefObject<HTMLDivElement | null>;
}) {
  return (
    <CardContent className="p-0">
      <div
        className="scrollbar-thin max-h-[32rem] overflow-y-auto bg-stealth-bg p-4 font-mono text-xs leading-6"
        aria-live="polite"
      >
        {error ? (
          <div className="mb-3 rounded-lg border border-rose-300/20 bg-rose-400/10 px-3 py-2 font-sans text-xs text-rose-200">
            {error}
          </div>
        ) : null}
        {lines.length ? (
          lines.map((line) => (
            <div key={line.sequence} className="stealth-log-line flex gap-3">
              <span className="w-32 shrink-0 text-slate-700">
                {formatDate(line.created_at)}
              </span>
              <span className={getLogLevelClass(line.level)}>
                {line.level.padEnd(5, " ")}
              </span>
              <span className="whitespace-pre-wrap text-slate-300">
                {line.message}
              </span>
            </div>
          ))
        ) : (
          <div className="py-16 text-center font-sans text-sm text-slate-600">
            {loading
              ? "Loading log lines…"
              : localCleared
                ? "Local view cleared. New lines will appear here."
                : emptyMessage}
          </div>
        )}
        <div ref={bottomRef} />
      </div>
    </CardContent>
  );
}

export function LogViewer({
  title = "Logs",
  description = "Incremental log stream",
  fetchPage,
  enabled = true,
  polling = enabled,
  emptyMessage = "No log lines returned yet.",
}: {
  title?: string;
  description?: string;
  fetchPage: (after?: number) => Promise<LogLine[]>;
  enabled?: boolean;
  polling?: boolean;
  emptyMessage?: string;
}) {
  const { lines, after, loading, error, localCleared, clearLocal } =
    useLogStream({ fetchPage, enabled, polling });
  const [search, setSearch] = useState("");
  const [autoFollow, setAutoFollow] = useState(true);
  const [copied, setCopied] = useState(false);
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (autoFollow) {
      bottomRef.current?.scrollIntoView({ behavior: "smooth" });
    }
  }, [autoFollow, lines]);

  const visible = useMemo(
    () =>
      lines.filter((line) =>
        `${line.level} ${line.message}`
          .toLowerCase()
          .includes(search.toLowerCase()),
      ),
    [lines, search],
  );

  const handleCopy = async () => {
    if (!lines.length) return;

    try {
      await navigator.clipboard.writeText(
        lines
          .map(
            (line) =>
              `[${formatDate(line.created_at)}] ${line.level.toUpperCase()} ${line.message}`,
          )
          .join("\n"),
      );
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1_600);
    } catch {
      setCopied(false);
    }
  };

  const handleJumpToLatest = () => {
    setAutoFollow(true);
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  };

  return (
    <Card className="overflow-hidden">
      <LogToolbar
        title={title}
        description={description}
        after={after}
        autoFollow={autoFollow}
        copied={copied}
        search={search}
        onSearchChange={setSearch}
        onToggleFollow={() => setAutoFollow((value) => !value)}
        onJumpToLatest={handleJumpToLatest}
        onCopy={() => void handleCopy()}
        onClear={clearLocal}
      />
      <LogBody
        lines={visible}
        error={error}
        loading={loading}
        localCleared={localCleared}
        emptyMessage={emptyMessage}
        bottomRef={bottomRef}
      />
    </Card>
  );
}
