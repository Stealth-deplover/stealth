"use client";

import { useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Search } from "lucide-react";
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
import { Input } from "@/components/ui/input";
import { formatDate } from "@/lib/format";
import { AdminShell } from "./admin-shell";
import { AdminTimeRange, useAdminTimeRange } from "./admin-time-range";

export function AdminLogsView() {
  const timeRange = useAdminTimeRange();
  const searchParams = useSearchParams();
  const [search, setSearch] = useState("");
  const [service, setService] = useState("");
  const [level, setLevel] = useState("");
  const [traceID, setTraceID] = useState(
    () => searchParams.get("trace_id") ?? "",
  );
  const [tailEnabled, setTailEnabled] = useState(false);
  const query = useMemo(
    () => ({
      ...timeRange.query,
      query: search,
      service,
      level,
      trace_id: traceID,
      limit: 100,
    }),
    [level, search, service, timeRange.query, traceID],
  );
  const logs = useAdminLogs(query, {
    refetchInterval: timeRange.refreshInterval,
  });
  const tail = useAdminLogTail(query, tailEnabled);
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
        <CardContent className="grid gap-3 p-4 md:grid-cols-[1fr_0.35fr_0.25fr_0.5fr]">
          <label className="relative block">
            <span className="sr-only">Search logs</span>
            <Search
              className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-ash"
              aria-hidden="true"
            />
            <Input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Search message text"
              className="pl-9"
            />
          </label>
          <label>
            <span className="sr-only">Service</span>
            <Input
              value={service}
              onChange={(event) => setService(event.target.value)}
              placeholder="Service"
            />
          </label>
          <label>
            <span className="sr-only">Level</span>
            <Input
              value={level}
              onChange={(event) => setLevel(event.target.value)}
              placeholder="Level"
            />
          </label>
          <label>
            <span className="sr-only">Trace ID</span>
            <Input
              value={traceID}
              onChange={(event) => setTraceID(event.target.value)}
              placeholder="Trace ID"
            />
          </label>
        </CardContent>
      </Card>
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
