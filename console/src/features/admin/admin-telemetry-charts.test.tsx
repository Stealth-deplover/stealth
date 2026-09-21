import { fireEvent, render, screen } from "@testing-library/react";
import { afterAll, beforeAll, describe, expect, it, vi } from "vitest";
import { AdminMetricKind } from "@/api/generated/schema";
import { AdminLogVolumeChart } from "./admin-log-volume-chart";
import { AdminMetricChart } from "./admin-metric-chart";

const { mockChart } = vi.hoisted(() => ({
  mockChart: {
    setOption: vi.fn(),
    resize: vi.fn(),
    dispose: vi.fn(),
  },
}));

vi.mock("echarts/core", () => ({
  init: vi.fn(() => mockChart),
  use: vi.fn(),
}));
vi.mock("echarts/charts", () => ({
  BarChart: {},
  LineChart: {},
}));
vi.mock("echarts/components", () => ({
  DataZoomComponent: {},
  GridComponent: {},
  LegendComponent: {},
  TooltipComponent: {},
}));
vi.mock("echarts/renderers", () => ({ CanvasRenderer: {} }));

class TestResizeObserver {
  observe() {}
  disconnect() {}
}

describe("Admin telemetry charts", () => {
  beforeAll(() => {
    vi.stubGlobal("ResizeObserver", TestResizeObserver);
  });

  afterAll(() => {
    vi.unstubAllGlobals();
  });

  it("provides a data table for log volume", () => {
    render(
      <AdminLogVolumeChart
        items={[
          { timestamp: "2026-09-20T00:00:00.000Z", count: 4 },
          { timestamp: "2026-09-20T00:05:00.000Z", count: 7 },
        ]}
      />,
    );

    expect(
      screen.getByRole("img", { name: "Log volume by time bucket" }),
    ).toHaveAttribute("aria-describedby");
    fireEvent.click(screen.getByText("View data table"));
    expect(
      screen.getByRole("table", { name: "Log volume data" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "7" })).toBeInTheDocument();
  });

  it("provides a data table for metric series", () => {
    render(
      <AdminMetricChart
        items={[
          {
            timestamp: "2026-09-20T00:00:00.000Z",
            name: "system.cpu.utilization",
            service: "telemetry-host",
            value: 0.42,
            kind: AdminMetricKind.gauge,
          },
        ]}
      />,
    );

    expect(
      screen.getByRole("img", { name: "Metric time series with 1 series" }),
    ).toHaveAttribute("aria-describedby");
    fireEvent.click(screen.getByText("View data table"));
    expect(
      screen.getByRole("table", { name: "Metric time series data" }),
    ).toBeInTheDocument();
    expect(screen.getByText("system.cpu.utilization")).toBeInTheDocument();
  });

  it("renders structured metric summaries without coercing them to scalars", () => {
    render(
      <AdminMetricChart
        items={[
          {
            timestamp: "2026-09-20T00:00:00.000Z",
            name: "http.server.request.duration",
            service: "api",
            kind: AdminMetricKind.histogram,
            histogram: {
              count: 3,
              sum: 6,
              bucket_counts: [1, 2],
              explicit_bounds: [1, 2],
              aggregation_temporality: 2,
            },
          },
        ]}
      />,
    );

    fireEvent.click(screen.getByText("View data table"));
    expect(screen.getByText("count=3 sum=6")).toBeInTheDocument();
    expect(
      screen.getByRole("img", { name: "Metric time series with 0 series" }),
    ).toBeInTheDocument();
  });
});
