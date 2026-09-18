"use client";

import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { cn } from "@/lib/utils";

export const adminRanges = [
  { key: "15m", label: "15m", milliseconds: 15 * 60_000 },
  { key: "1h", label: "1h", milliseconds: 60 * 60_000 },
  { key: "6h", label: "6h", milliseconds: 6 * 60 * 60_000 },
  { key: "24h", label: "24h", milliseconds: 24 * 60 * 60_000 },
  { key: "7d", label: "7d", milliseconds: 7 * 24 * 60 * 60_000 },
  { key: "30d", label: "30d", milliseconds: 30 * 24 * 60 * 60_000 },
] as const;

export const adminRefreshIntervals = [
  { key: "off", label: "Off", milliseconds: false as const },
  { key: "5s", label: "5s", milliseconds: 5_000 },
  { key: "10s", label: "10s", milliseconds: 10_000 },
  { key: "30s", label: "30s", milliseconds: 30_000 },
  { key: "1m", label: "1m", milliseconds: 60_000 },
] as const;

type RangeKey = (typeof adminRanges)[number]["key"];
type CustomRangeKey = "custom";
type AnyRangeKey = RangeKey | CustomRangeKey;
type RefreshKey = (typeof adminRefreshIntervals)[number]["key"];

const rangeStorageKey = "stealth.admin.time-range";
const customFromStorageKey = "stealth.admin.custom-from";
const customToStorageKey = "stealth.admin.custom-to";
const refreshStorageKey = "stealth.admin.refresh-interval";
const preferenceEvent = "stealth-admin-preference-change";

function subscribeToPreferences(onChange: () => void) {
  if (typeof window === "undefined") return () => undefined;
  window.addEventListener("storage", onChange);
  window.addEventListener(preferenceEvent, onChange);
  return () => {
    window.removeEventListener("storage", onChange);
    window.removeEventListener(preferenceEvent, onChange);
  };
}

function readStoredRange(): AnyRangeKey {
  if (typeof window === "undefined") return "1h";
  try {
    const saved = window.localStorage.getItem(rangeStorageKey);
    return saved === "custom" || adminRanges.some((item) => item.key === saved)
      ? (saved as AnyRangeKey)
      : "1h";
  } catch {
    return "1h";
  }
}

function readStoredCustomRange() {
  if (typeof window === "undefined") return undefined;
  try {
    const from = window.localStorage.getItem(customFromStorageKey);
    const to = window.localStorage.getItem(customToStorageKey);
    if (!from || !to) return undefined;
    const fromDate = new Date(from);
    const toDate = new Date(to);
    if (
      !Number.isFinite(fromDate.getTime()) ||
      !Number.isFinite(toDate.getTime()) ||
      toDate.getTime() <= fromDate.getTime()
    ) {
      return undefined;
    }
    if (toDate.getTime() - fromDate.getTime() > 30 * 24 * 60 * 60_000) {
      return undefined;
    }
    return { from: fromDate.toISOString(), to: toDate.toISOString() };
  } catch {
    return undefined;
  }
}

function readStoredRefresh(): RefreshKey {
  if (typeof window === "undefined") return "off";
  try {
    const saved = window.localStorage.getItem(refreshStorageKey);
    return adminRefreshIntervals.some((item) => item.key === saved)
      ? (saved as RefreshKey)
      : "off";
  } catch {
    return "off";
  }
}

export function useAdminTimeRange() {
  const rangeKey = useSyncExternalStore(
    subscribeToPreferences,
    readStoredRange,
    () => "1h" as AnyRangeKey,
  );
  const refreshKey = useSyncExternalStore(
    subscribeToPreferences,
    readStoredRefresh,
    () => "off" as RefreshKey,
  );
  const selectedRefresh = adminRefreshIntervals.find(
    (item) => item.key === refreshKey,
  )!;
  const [refreshTick, setRefreshTick] = useState(0);

  useEffect(() => {
    if (selectedRefresh.milliseconds === false) return;
    const timer = window.setInterval(
      () => setRefreshTick((value) => value + 1),
      selectedRefresh.milliseconds,
    );
    return () => window.clearInterval(timer);
  }, [selectedRefresh.milliseconds]);

  const customRange = readStoredCustomRange();
  const selectedRange = adminRanges.find((item) => item.key === rangeKey);
  const query = useMemo(() => {
    if (rangeKey === "custom" && customRange) return customRange;
    const to = new Date();
    return {
      from: new Date(
        to.getTime() - (selectedRange?.milliseconds ?? 60 * 60_000),
      ).toISOString(),
      to: to.toISOString(),
    };
    // refreshTick intentionally invalidates the moving window when auto-refresh is on.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [customRange, rangeKey, selectedRange?.milliseconds, refreshTick]);

  const persist = (key: string, value: string) => {
    try {
      window.localStorage.setItem(key, value);
    } catch {
      // A private browsing context can reject localStorage. The preference
      // remains valid for the current request lifecycle.
    }
    window.dispatchEvent(new Event(preferenceEvent));
  };

  return {
    query,
    rangeKey,
    refreshKey,
    refreshInterval: selectedRefresh.milliseconds,
    setRange: (next: AnyRangeKey) => {
      persist(rangeStorageKey, next);
    },
    setRefresh: (next: RefreshKey) => {
      persist(refreshStorageKey, next);
    },
  };
}

export function AdminTimeRange({
  rangeKey,
  refreshKey,
  onRangeChange,
  onRefreshChange,
}: {
  rangeKey: AnyRangeKey;
  refreshKey: RefreshKey;
  onRangeChange: (value: AnyRangeKey) => void;
  onRefreshChange: (value: RefreshKey) => void;
}) {
  const initialCustom = readStoredCustomRange();
  const [customFrom, setCustomFrom] = useState(() =>
    toDateTimeInput(
      initialCustom?.from ?? new Date(Date.now() - 60 * 60_000).toISOString(),
    ),
  );
  const [customTo, setCustomTo] = useState(() =>
    toDateTimeInput(initialCustom?.to ?? new Date().toISOString()),
  );
  const [customError, setCustomError] = useState("");
  const displayRanges = [
    ...adminRanges,
    { key: "custom" as const, label: "Custom" },
  ];
  const applyCustom = () => {
    const from = new Date(customFrom);
    const to = new Date(customTo);
    if (
      !Number.isFinite(from.getTime()) ||
      !Number.isFinite(to.getTime()) ||
      to.getTime() <= from.getTime()
    ) {
      setCustomError("Choose a valid time range.");
      return;
    }
    if (to.getTime() - from.getTime() > 30 * 24 * 60 * 60_000) {
      setCustomError("Custom range cannot exceed 30 days.");
      return;
    }
    setCustomError("");
    try {
      window.localStorage.setItem(customFromStorageKey, from.toISOString());
      window.localStorage.setItem(customToStorageKey, to.toISOString());
    } catch {
      // The range is still applied for this render; shared preference storage is optional.
    }
    window.dispatchEvent(new Event(preferenceEvent));
    onRangeChange("custom");
  };
  return (
    <div
      className="flex flex-wrap items-center gap-3"
      aria-label="Telemetry time range"
    >
      <div className="flex items-center gap-1 rounded-md border border-graphite bg-carbon p-1">
        {displayRanges.map((item) => (
          <button
            key={item.key}
            type="button"
            aria-pressed={rangeKey === item.key}
            onClick={() => onRangeChange(item.key)}
            className={cn(
              "rounded-xs px-2 py-1 text-xs text-fog transition-colors duration-150 hover:text-mist",
              rangeKey === item.key && "bg-white/[0.08] text-paper",
            )}
          >
            {item.label}
          </button>
        ))}
      </div>
      {rangeKey === "custom" ? (
        <div className="flex flex-wrap items-end gap-2 rounded-md border border-graphite bg-carbon p-2">
          <label className="text-[11px] text-fog">
            From (local time)
            <input
              type="datetime-local"
              value={customFrom}
              onChange={(event) => setCustomFrom(event.target.value)}
              className="mt-1 block h-8 rounded-md border border-graphite bg-void px-2 text-xs text-mist outline-none focus:border-smoke"
            />
          </label>
          <label className="text-[11px] text-fog">
            To (local time)
            <input
              type="datetime-local"
              value={customTo}
              onChange={(event) => setCustomTo(event.target.value)}
              className="mt-1 block h-8 rounded-md border border-graphite bg-void px-2 text-xs text-mist outline-none focus:border-smoke"
            />
          </label>
          <button
            type="button"
            onClick={applyCustom}
            className="h-8 rounded-md bg-acid-lime px-3 text-xs font-medium text-void transition-colors duration-150 hover:bg-acid-lime/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-acid-lime/60"
          >
            Apply
          </button>
          {customError ? (
            <p className="basis-full text-[11px] text-coral-red" role="alert">
              {customError}
            </p>
          ) : null}
        </div>
      ) : null}
      <label className="flex items-center gap-2 text-xs text-fog">
        Refresh
        <select
          value={refreshKey}
          onChange={(event) =>
            onRefreshChange(event.target.value as RefreshKey)
          }
          className="h-8 rounded-md border border-graphite bg-carbon px-2 text-xs text-mist outline-none focus:border-smoke"
        >
          {adminRefreshIntervals.map((item) => (
            <option key={item.key} value={item.key}>
              {item.label}
            </option>
          ))}
        </select>
      </label>
    </div>
  );
}

function toDateTimeInput(value: string) {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "";
  const pad = (part: number) => String(part).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}
