import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AdminTimeRange, useAdminTimeRange } from "./admin-time-range";

function Harness() {
  const timeRange = useAdminTimeRange();
  return (
    <>
      <AdminTimeRange
        rangeKey={timeRange.rangeKey}
        refreshKey={timeRange.refreshKey}
        customRange={timeRange.customRange}
        onRangeChange={timeRange.setRange}
        onRefreshChange={timeRange.setRefresh}
        onCustomRangeChange={timeRange.setCustomRange}
      />
      <output data-testid="range">{timeRange.rangeKey}</output>
      <output data-testid="refresh">{timeRange.refreshKey}</output>
      <output data-testid="from">{timeRange.query.from}</output>
      <output data-testid="to">{timeRange.query.to}</output>
    </>
  );
}

describe("admin time range preferences", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("persists selected range and refresh interval without losing the active values", () => {
    render(<Harness />);

    fireEvent.click(screen.getByRole("button", { name: "24h" }));
    fireEvent.change(screen.getByLabelText("Refresh"), {
      target: { value: "30s" },
    });

    expect(screen.getByTestId("range")).toHaveTextContent("24h");
    expect(screen.getByTestId("refresh")).toHaveTextContent("30s");
    expect(window.localStorage.getItem("stealth.admin.time-range")).toBe("24h");
    expect(window.localStorage.getItem("stealth.admin.refresh-interval")).toBe(
      "30s",
    );
  });

  it("re-reads preferences after a remount", () => {
    window.localStorage.setItem("stealth.admin.time-range", "7d");
    window.localStorage.setItem("stealth.admin.refresh-interval", "1m");

    render(<Harness />);

    expect(screen.getByTestId("range")).toHaveTextContent("7d");
    expect(screen.getByTestId("refresh")).toHaveTextContent("1m");
  });

  it("moves a refresh window forward on the configured interval", () => {
    vi.useFakeTimers();
    try {
      vi.setSystemTime(new Date("2026-09-17T00:00:00.000Z"));
      render(<Harness />);
      const initialTo = screen.getByTestId("to").textContent;
      fireEvent.change(screen.getByLabelText("Refresh"), {
        target: { value: "5s" },
      });

      act(() => vi.advanceTimersByTime(5_000));
      expect(screen.getByTestId("to")).not.toHaveTextContent(initialTo ?? "");
    } finally {
      vi.useRealTimers();
    }
  });

  it("persists and restores a bounded custom range", () => {
    render(<Harness />);

    fireEvent.click(screen.getByRole("button", { name: "Custom" }));
    fireEvent.change(screen.getByLabelText("From (local time)"), {
      target: { value: "2026-09-17T10:00" },
    });
    fireEvent.change(screen.getByLabelText("To (local time)"), {
      target: { value: "2026-09-17T11:30" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));

    expect(screen.getByTestId("range")).toHaveTextContent("custom");
    expect(window.localStorage.getItem("stealth.admin.time-range")).toBe(
      "custom",
    );
    expect(window.localStorage.getItem("stealth.admin.custom-from")).toBe(
      new Date("2026-09-17T10:00").toISOString(),
    );
    expect(window.localStorage.getItem("stealth.admin.custom-to")).toBe(
      new Date("2026-09-17T11:30").toISOString(),
    );
  });

  it("keeps the selected range in memory when browser storage is unavailable", () => {
    const getItem = vi
      .spyOn(Storage.prototype, "getItem")
      .mockImplementation(() => {
        throw new DOMException("Storage unavailable");
      });
    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new DOMException("Storage unavailable");
      });

    try {
      render(<Harness />);

      fireEvent.click(screen.getByRole("button", { name: "24h" }));
      fireEvent.change(screen.getByLabelText("Refresh"), {
        target: { value: "30s" },
      });

      expect(screen.getByTestId("range")).toHaveTextContent("24h");
      expect(screen.getByTestId("refresh")).toHaveTextContent("30s");

      fireEvent.click(screen.getByRole("button", { name: "Custom" }));
      fireEvent.change(screen.getByLabelText("From (local time)"), {
        target: { value: "2026-09-17T10:00" },
      });
      fireEvent.change(screen.getByLabelText("To (local time)"), {
        target: { value: "2026-09-17T11:30" },
      });
      fireEvent.click(screen.getByRole("button", { name: "Apply" }));

      expect(screen.getByTestId("range")).toHaveTextContent("custom");
      expect(screen.getByTestId("from")).toHaveTextContent(
        new Date("2026-09-17T10:00").toISOString(),
      );
    } finally {
      getItem.mockRestore();
      setItem.mockRestore();
    }
  });

  it("gives every time-range control a standard touch target", () => {
    render(<Harness />);

    expect(
      screen.getByRole("group", { name: "Telemetry time range" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "24h" })).toHaveClass(
      "min-h-11",
      "min-w-11",
    );
    expect(screen.getByLabelText("Refresh")).toHaveClass("min-h-11");
    expect(screen.getByLabelText("Refresh")).toHaveClass(
      "focus:border-acid-lime",
      "focus-visible:ring-2",
    );

    fireEvent.click(screen.getByRole("button", { name: "Custom" }));

    expect(screen.getByLabelText("From (local time)")).toHaveClass(
      "min-h-11",
      "focus:border-acid-lime",
      "focus-visible:ring-2",
    );
    expect(screen.getByLabelText("To (local time)")).toHaveClass(
      "min-h-11",
      "focus:border-acid-lime",
      "focus-visible:ring-2",
    );
    expect(screen.getByRole("button", { name: "Apply" })).toHaveClass(
      "min-h-11",
    );
  });

  it("rejects a custom range whose end is not after its start", () => {
    render(<Harness />);

    fireEvent.click(screen.getByRole("button", { name: "Custom" }));
    fireEvent.change(screen.getByLabelText("From (local time)"), {
      target: { value: "2026-09-17T11:00" },
    });
    fireEvent.change(screen.getByLabelText("To (local time)"), {
      target: { value: "2026-09-17T10:00" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));

    expect(screen.getByRole("alert")).toHaveTextContent(
      "Choose a valid time range.",
    );
    expect(screen.getByTestId("range")).toHaveTextContent("custom");
  });
});
