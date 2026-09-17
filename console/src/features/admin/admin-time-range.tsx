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
type RefreshKey = (typeof adminRefreshIntervals)[number]["key"];

const rangeStorageKey = "stealth.admin.time-range";
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

function readStoredRange(): RangeKey {
  if (typeof window === "undefined") return "1h";
  try {
    const saved = window.localStorage.getItem(rangeStorageKey);
    return adminRanges.some((item) => item.key === saved)
      ? (saved as RangeKey)
      : "1h";
  } catch {
    return "1h";
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
    () => "1h" as RangeKey,
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

  const selectedRange = adminRanges.find((item) => item.key === rangeKey)!;
  const query = useMemo(() => {
    const to = new Date();
    return {
      from: new Date(to.getTime() - selectedRange.milliseconds).toISOString(),
      to: to.toISOString(),
    };
    // refreshTick intentionally invalidates the moving window when auto-refresh is on.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedRange.milliseconds, refreshTick]);

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
    setRange: (next: RangeKey) => {
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
  rangeKey: RangeKey;
  refreshKey: RefreshKey;
  onRangeChange: (value: RangeKey) => void;
  onRefreshChange: (value: RefreshKey) => void;
}) {
  return (
    <div
      className="flex flex-wrap items-center gap-3"
      aria-label="Telemetry time range"
    >
      <div className="flex items-center gap-1 rounded-md border border-graphite bg-carbon p-1">
        {adminRanges.map((item) => (
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
