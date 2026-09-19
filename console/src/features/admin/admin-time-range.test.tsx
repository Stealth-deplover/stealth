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
