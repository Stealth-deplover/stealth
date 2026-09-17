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
        onRangeChange={timeRange.setRange}
        onRefreshChange={timeRange.setRefresh}
      />
      <output data-testid="range">{timeRange.rangeKey}</output>
      <output data-testid="refresh">{timeRange.refreshKey}</output>
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
});
