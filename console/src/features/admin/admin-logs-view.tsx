"use client";

import { useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Download, History, Search, X } from "lucide-react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { useAdminLogTail, useAdminLogs } from "@/api/queries";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { formatDate } from "@/lib/format";
import { AdminShell } from "./admin-shell";
import { formatAdminLogQuery, parseAdminLogQuery } from "./admin-log-query";
import { AdminQueryEditor } from "./admin-query-editor";
import { AdminTimeRange, useAdminTimeRange } from "./admin-time-range";

const logQueryHistoryKey = "stealth.admin.log-query-history";

function readLogQueryHistory(): string[] {
  if (typeof window === "undefined") return [];
  try {
    const parsed = JSON.parse(
      window.localStorage.getItem(logQueryHistoryKey) ?? "[]",
    );
    if (!Array.isArray(parsed)) return [];
    return parsed
      .filter((item): item is string => typeof item === "string")
      .slice(0, 8);
  } catch {
    return [];
  }
}

export function AdminLogsView() {
  const timeRange = useAdminTimeRange();
  const searchParams = useSearchParams();
  const initialQuery = useMemo(
    () =>
      formatAdminLogQuery({
        trace_id: searchParams.get("trace_id") ?? "",
      }),
    [searchParams],
  );
  const [draftQuery, setDraftQuery] = useState(initialQuery);
  const [activeQuery, setActiveQuery] = useState(initialQuery);
  const [queryHistory, setQueryHistory] = useState(readLogQueryHistory);
  const [tailEnabled, setTailEnabled] = useState(false);
  const draftParse = useMemo(
    () => parseAdminLogQuery(draftQuery),
    [draftQuery],
  );
  const activeParse = useMemo(
    () => parseAdminLogQuery(activeQuery),
    [activeQuery],
  );
  const query = useMemo(
    () => ({
      ...timeRange.query,
      ...activeParse.filters,
      limit: 100,
    }),
    [activeParse.filters, timeRange.query],
  );
  const logs = useAdminLogs(query, {
    enabled: activeParse.valid,
    refetchInterval: timeRange.refreshInterval,
  });
  const tail = useAdminLogTail(query, tailEnabled && activeParse.valid);
  const displayItems = tailEnabled
    ? tail.items.length
      ? tail.items
      : (logs.data?.items ?? [])
    : (logs.data?.items ?? []);

  return (
    <AdminShell>
      <PageHeader
        eyebrow="Admin / Telemetry"
        title="Logs"
        description="Search structured logs collected from the Stealth runtime and its Docker services."
        actions={
          <div className="flex flex-wrap items-center justify-end gap-2">
            <Button
              type="button"
              size="sm"
              variant={tailEnabled ? "secondary" : "ghost"}
              aria-pressed={tailEnabled}
              onClick={() => setTailEnabled((enabled) => !enabled)}
            >
              <span
                className={`size-1.5 rounded-full ${tail.connected ? "bg-pulse-green" : "bg-ash"}`}
                aria-hidden="true"
              />
              {tailEnabled ? "Live tail on" : "Live tail"}
            </Button>
            <AdminTimeRange
              rangeKey={timeRange.rangeKey}
              refreshKey={timeRange.refreshKey}
              onRangeChange={timeRange.setRange}
              onRefreshChange={timeRange.setRefresh}
            />
          </div>
        }
      />
      {tailEnabled ? (
        <div className="mb-4 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-fog">
          <span>
            {tail.connected
              ? "Streaming new records"
              : "Connecting to live stream"}
          </span>
          {tail.error ? (
            <span className="text-coral-red">{tail.error}</span>
          ) : null}
        </div>
      ) : null}
      <Card className="mb-4">
        <CardContent className="space-y-3 p-4">
          <div className="flex items-start gap-3">
            <Search
              className="mt-3 size-4 shrink-0 text-ash"
              aria-hidden="true"
            />
            <div className="min-w-0 flex-1">
              <label className="mb-2 block text-xs uppercase tracking-[0.1em] text-fog">
                Structured query
              </label>
              <AdminQueryEditor
                value={draftQuery}
                onChange={setDraftQuery}
                onSubmit={() => applyLogQuery(draftQuery, draftParse.valid)}
                invalid={!draftParse.valid}
              />
            </div>
            <Button
              type="button"
              size="sm"
              variant="default"
              onClick={() => applyLogQuery(draftQuery, draftParse.valid)}
              disabled={!draftParse.valid}
            >
              Apply
            </Button>
          </div>
          <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-fog">
            <p>
              Use{" "}
              <code className="font-mono text-mist">
                service:api level:ERROR
              </code>
              , <code className="font-mono text-mist">trace_id:…</code>, or{" "}
              <code className="font-mono text-mist">
                message:&quot;timeout&quot;
              </code>
              . Press Ctrl/Cmd+Enter to apply.
            </p>
            <div className="flex items-center gap-2">
              {queryHistory.length ? (
                <label className="flex items-center gap-1.5">
                  <History className="size-3.5" aria-hidden="true" />
                  <span className="sr-only">Recent log queries</span>
                  <select
                    value=""
                    onChange={(event) => {
                      const next = event.target.value;
                      if (next) setDraftQuery(next);
                    }}
                    className="max-w-[220px] rounded-md border border-graphite bg-carbon px-2 py-1.5 text-xs text-mist focus:border-acid-lime/70 focus:outline-none"
                  >
                    <option value="">History</option>
                    {queryHistory.map((item) => (
                      <option key={item} value={item}>
                        {item}
                      </option>
                    ))}
                  </select>
                </label>
              ) : null}
              <Button
                type="button"
                size="sm"
                variant="ghost"
                onClick={() => {
                  setDraftQuery("");
                  applyLogQuery("", true);
                }}
              >
                <X className="size-3.5" aria-hidden="true" />
                Clear
              </Button>
            </div>
          </div>
          {draftParse.diagnostics.map((diagnostic) => (
            <p
              key={`${diagnostic.from}-${diagnostic.message}`}
              className="text-xs text-coral-red"
            >
              {diagnostic.message}
            </p>
          ))}
        </CardContent>
      </Card>
      {logs.data?.items.length ? (
        <div className="mb-3 flex justify-end">
          <Button
            type="button"
            size="sm"
            variant="ghost"
            onClick={() => downloadLogs(logs.data?.items ?? [])}
          >
            <Download className="size-3.5" aria-hidden="true" />
            Download current result
          </Button>
        </div>
      ) : null}
      {logs.isPending ? <LoadingState rows={6} /> : null}
      {logs.error ? (
        <ErrorState
          title="Could not load logs"
          error={logs.error}
          retry={() => logs.refetch()}
        />
      ) : null}
      {logs.data && !displayItems.length ? (
        <Card>
          <CardContent className="p-8 text-center text-sm text-fog">
            No logs matched the selected filters.
          </CardContent>
        </Card>
      ) : null}
      {displayItems.length ? (
        <Card className="overflow-hidden">
          <VirtualizedLogTable items={displayItems} />
        </Card>
      ) : null}
    </AdminShell>
  );

  function applyLogQuery(next: string, valid: boolean) {
    if (!valid) return;
    setActiveQuery(next);
    const trimmed = next.trim();
    if (!trimmed) return;
    setQueryHistory((current) => {
      const nextHistory = [
        trimmed,
        ...current.filter((item) => item !== trimmed),
      ].slice(0, 8);
      try {
        window.localStorage.setItem(
          logQueryHistoryKey,
          JSON.stringify(nextHistory),
        );
      } catch {
        // History is optional and must not block log exploration.
      }
      return nextHistory;
    });
  }
}

function downloadLogs(items: AdminLogItem[]) {
  const payload = items.map((item) => ({
    timestamp: item.timestamp,
    service: item.service,
    level: item.level ?? "unknown",
    message: item.message,
    trace_id: item.trace_id ?? null,
    span_id: item.span_id ?? null,
    attributes: item.attributes ?? {},
    resource_attributes: item.resource_attributes ?? {},
  }));
  const blob = new Blob([JSON.stringify(payload, null, 2)], {
    type: "application/json",
  });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `stealth-logs-${new Date().toISOString().replaceAll(":", "-")}.json`;
  link.click();
  URL.revokeObjectURL(url);
}

type AdminLogItem = {
  timestamp: string;
  trace_id?: string;
  span_id?: string;
  level?: string;
  service: string;
  message: string;
  attributes?: Record<string, string>;
  resource_attributes?: Record<string, string>;
};

function VirtualizedLogTable({ items }: { items: AdminLogItem[] }) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [selected, setSelected] = useState<AdminLogItem | null>(null);
  // TanStack Virtual intentionally exposes an imperative virtualizer instance;
  // React Compiler must not memoize that object as if it were render data.
  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 72,
    overscan: 8,
    getItemKey: (index) =>
      `${items[index].timestamp}-${items[index].trace_id ?? ""}-${index}`,
  });

  return (
    <>
      <div ref={scrollRef} className="max-h-[620px] overflow-auto">
        <div
          role="table"
          aria-label="Structured logs"
          aria-rowcount={items.length + 1}
          className="min-w-[760px] text-left text-sm"
        >
          <div
            role="row"
            className="sticky top-0 z-10 grid grid-cols-[150px_130px_90px_minmax(300px,1fr)_170px] border-b border-graphite bg-carbon text-xs uppercase tracking-[0.1em] text-fog"
          >
            <div role="columnheader" className="px-4 py-3 font-medium">
              Time
            </div>
            <div role="columnheader" className="px-4 py-3 font-medium">
              Service
            </div>
            <div role="columnheader" className="px-4 py-3 font-medium">
              Level
            </div>
            <div role="columnheader" className="px-4 py-3 font-medium">
              Message
            </div>
            <div role="columnheader" className="px-4 py-3 font-medium">
              Trace
            </div>
          </div>
          <div
            style={{
              height: `${virtualizer.getTotalSize()}px`,
              position: "relative",
            }}
          >
            {virtualizer.getVirtualItems().map((virtualRow) => {
              const item = items[virtualRow.index];
              return (
                <button
                  key={virtualRow.key}
                  type="button"
                  role="row"
                  className="absolute left-0 grid h-[72px] w-full grid-cols-[150px_130px_90px_minmax(300px,1fr)_170px] border-b border-graphite text-left align-top transition-colors duration-150 hover:bg-white/[0.025] focus-visible:bg-white/[0.04]"
                  style={{
                    top: 0,
                    transform: `translateY(${virtualRow.start}px)`,
                  }}
                  onClick={() => setSelected(item)}
                  aria-label={`Open log from ${item.service}`}
                >
                  <span className="whitespace-nowrap px-4 py-3 font-mono text-xs text-fog">
                    {formatDate(item.timestamp)}
                  </span>
                  <span className="truncate px-4 py-3 text-mist">
                    {item.service}
                  </span>
                  <span className="px-4 py-3">
                    <Badge variant="neutral">{item.level ?? "unknown"}</Badge>
                  </span>
                  <span
                    className="truncate px-4 py-3 text-mist"
                    title={item.message}
                  >
                    {item.message}
                  </span>
                  <span className="truncate px-4 py-3 font-mono text-xs text-fog">
                    {item.trace_id ?? "none"}
                  </span>
                </button>
              );
            })}
          </div>
        </div>
      </div>
      <Dialog
        open={selected !== null}
        onOpenChange={(open) => !open && setSelected(null)}
      >
        <DialogHeader>
          <DialogTitle>Log detail</DialogTitle>
          <DialogDescription>
            Structured fields are redacted at the telemetry query boundary.
          </DialogDescription>
        </DialogHeader>
        {selected ? <LogDetail item={selected} /> : null}
      </Dialog>
    </>
  );
}

function LogDetail({ item }: { item: AdminLogItem }) {
  return (
    <div className="space-y-5 text-sm">
      <div className="grid gap-3 sm:grid-cols-2">
        <LogField label="Time" value={formatDate(item.timestamp)} mono />
        <LogField label="Service" value={item.service} />
        <LogField label="Level" value={item.level ?? "unknown"} />
        <LogField
          label="Trace ID"
          value={item.trace_id ?? "none"}
          mono
          href={
            item.trace_id
              ? `/admin/telemetry/traces?trace_id=${encodeURIComponent(item.trace_id)}`
              : undefined
          }
        />
        <LogField label="Span ID" value={item.span_id ?? "none"} mono />
      </div>
      <div>
        <p className="mb-2 text-xs uppercase tracking-[0.1em] text-fog">
          Message
        </p>
        <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-md border border-graphite bg-void p-3 font-mono text-xs leading-5 text-mist">
          {item.message}
        </pre>
      </div>
      <AttributeGroup label="Attributes" values={item.attributes} />
      <AttributeGroup
        label="Resource attributes"
        values={item.resource_attributes}
      />
    </div>
  );
}

function LogField({
  label,
  value,
  mono = false,
  href,
}: {
  label: string;
  value: string;
  mono?: boolean;
  href?: string;
}) {
  return (
    <div className="min-w-0">
      <p className="text-xs uppercase tracking-[0.1em] text-fog">{label}</p>
      {href ? (
        <Link
          href={href}
          className={`mt-1 block truncate text-mist underline decoration-graphite underline-offset-4 hover:text-paper ${mono ? "font-mono text-xs" : ""}`}
        >
          {value}
        </Link>
      ) : (
        <p
          className={`mt-1 truncate text-mist ${mono ? "font-mono text-xs" : ""}`}
        >
          {value}
        </p>
      )}
    </div>
  );
}

function AttributeGroup({
  label,
  values,
}: {
  label: string;
  values?: Record<string, string>;
}) {
  const entries = Object.entries(values ?? {});
  if (!entries.length) return null;
  return (
    <div>
      <p className="mb-2 text-xs uppercase tracking-[0.1em] text-fog">
        {label}
      </p>
      <div className="divide-y divide-graphite rounded-md border border-graphite">
        {entries.map(([key, value]) => (
          <div
            key={key}
            className="grid gap-2 px-3 py-2 sm:grid-cols-[0.8fr_1.2fr]"
          >
            <span className="truncate font-mono text-xs text-fog">{key}</span>
            <span className="break-words font-mono text-xs text-mist">
              {value}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}
