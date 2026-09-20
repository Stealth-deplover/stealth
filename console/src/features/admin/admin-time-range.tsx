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
type CustomRange = { from: string; to: string };
type StoredPreferences = {
  rangeKey: AnyRangeKey;
  refreshKey: RefreshKey;
  customRange?: CustomRange;
};

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

function readStoredPreferences(): StoredPreferences {
  return {
    rangeKey: readStoredRange(),
    refreshKey: readStoredRefresh(),
    customRange: readStoredCustomRange(),
  };
}

function readStoredPreferencesSnapshot() {
  return JSON.stringify(readStoredPreferences());
}

const serverPreferencesSnapshot = JSON.stringify({
  rangeKey: "1h" satisfies AnyRangeKey,
  refreshKey: "off" satisfies RefreshKey,
});

function parseStoredPreferences(snapshot: string): StoredPreferences {
  return JSON.parse(snapshot) as StoredPreferences;
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
  const storedPreferencesSnapshot = useSyncExternalStore(
    subscribeToPreferences,
    readStoredPreferencesSnapshot,
    () => serverPreferencesSnapshot,
  );
  const storedPreferences = parseStoredPreferences(storedPreferencesSnapshot);
  const [sessionPreferences, setSessionPreferences] = useState<
    Partial<StoredPreferences>
  >({});

  const rangeKey = sessionPreferences.rangeKey ?? storedPreferences.rangeKey;
  const refreshKey =
    sessionPreferences.refreshKey ?? storedPreferences.refreshKey;
  const customRange =
    sessionPreferences.customRange ?? storedPreferences.customRange;
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

  const selectedRange = adminRanges.find((item) => item.key === rangeKey);
  const query = useMemo(() => {
    if (rangeKey === "custom" && customRange) {
      return customRange;
    }
    const to = new Date();
    return {
      from: new Date(
        to.getTime() - (selectedRange?.milliseconds ?? 60 * 60_000),
      ).toISOString(),
      to: to.toISOString(),
    };
    // refreshTick intentionally invalidates the moving window when auto-refresh is on.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    customRange?.from,
    customRange?.to,
    rangeKey,
    selectedRange?.milliseconds,
    refreshTick,
  ]);

  const persist = (values: Array<[string, string]>) => {
    try {
      for (const [key, value] of values) {
        window.localStorage.setItem(key, value);
      }
      return true;
    } catch {
      // The active preference is held in React state. Persistence is optional.
      return false;
    } finally {
      window.dispatchEvent(new Event(preferenceEvent));
    }
  };

  const updateSessionPreferences = (next: Partial<StoredPreferences>) => {
    setSessionPreferences((current) => ({ ...current, ...next }));
  };

  const clearSessionPreferences = (keys: Array<keyof StoredPreferences>) => {
    setSessionPreferences((current) => {
      const next = { ...current };
      for (const key of keys) delete next[key];
      return next;
    });
  };

  return {
    query,
    rangeKey,
    refreshKey,
    customRange,
    refreshInterval: selectedRefresh.milliseconds,
    setRange: (next: AnyRangeKey) => {
      if (persist([[rangeStorageKey, next]])) {
        clearSessionPreferences(["rangeKey"]);
      } else {
        updateSessionPreferences({ rangeKey: next });
      }
    },
    setRefresh: (next: RefreshKey) => {
      if (persist([[refreshStorageKey, next]])) {
        clearSessionPreferences(["refreshKey"]);
      } else {
        updateSessionPreferences({ refreshKey: next });
      }
    },
    setCustomRange: (next: CustomRange) => {
      if (
        persist([
          [customFromStorageKey, next.from],
          [customToStorageKey, next.to],
          [rangeStorageKey, "custom"],
        ])
      ) {
        clearSessionPreferences(["rangeKey", "customRange"]);
      } else {
        updateSessionPreferences({ rangeKey: "custom", customRange: next });
      }
    },
  };
}

export function AdminTimeRange({
  rangeKey,
  refreshKey,
  customRange,
  onRangeChange,
  onRefreshChange,
  onCustomRangeChange,
}: {
  rangeKey: AnyRangeKey;
  refreshKey: RefreshKey;
  customRange?: CustomRange;
  onRangeChange: (value: AnyRangeKey) => void;
  onRefreshChange: (value: RefreshKey) => void;
  onCustomRangeChange: (value: CustomRange) => void;
}) {
  const [customFrom, setCustomFrom] = useState(() =>
    toDateTimeInput(
      customRange?.from ?? new Date(Date.now() - 60 * 60_000).toISOString(),
    ),
  );
  const [customTo, setCustomTo] = useState(() =>
    toDateTimeInput(customRange?.to ?? new Date().toISOString()),
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
    onCustomRangeChange({ from: from.toISOString(), to: to.toISOString() });
  };
  return (
    <div
      className="flex flex-wrap items-center gap-3"
      role="group"
      aria-label="Telemetry time range"
    >
      <div className="flex flex-wrap items-center gap-1 rounded-md border border-graphite bg-carbon p-1">
        {displayRanges.map((item) => (
          <button
            key={item.key}
            type="button"
            aria-pressed={rangeKey === item.key}
            onClick={() => {
              if (item.key === "custom") {
                setCustomFrom(
                  toDateTimeInput(
                    customRange?.from ??
                      new Date(Date.now() - 60 * 60_000).toISOString(),
                  ),
                );
                setCustomTo(
                  toDateTimeInput(customRange?.to ?? new Date().toISOString()),
                );
              }
              onRangeChange(item.key);
            }}
            className={cn(
              "min-h-11 min-w-11 rounded-xs px-2 py-1 text-xs text-fog transition-colors duration-150 hover:text-mist",
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
              className="mt-1 block min-h-11 rounded-md border border-graphite bg-void px-2 text-xs text-mist focus:border-acid-lime focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-acid-lime/60 focus-visible:ring-offset-2 focus-visible:ring-offset-void"
            />
          </label>
          <label className="text-[11px] text-fog">
            To (local time)
            <input
              type="datetime-local"
              value={customTo}
              onChange={(event) => setCustomTo(event.target.value)}
              className="mt-1 block min-h-11 rounded-md border border-graphite bg-void px-2 text-xs text-mist focus:border-acid-lime focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-acid-lime/60 focus-visible:ring-offset-2 focus-visible:ring-offset-void"
            />
          </label>
          <button
            type="button"
            onClick={applyCustom}
            className="min-h-11 rounded-md bg-acid-lime px-3 text-xs font-medium text-void transition-colors duration-150 hover:bg-acid-lime/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-acid-lime/60"
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
          className="min-h-11 rounded-md border border-graphite bg-carbon px-2 text-xs text-mist focus:border-acid-lime focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-acid-lime/60 focus-visible:ring-offset-2 focus-visible:ring-offset-void"
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
