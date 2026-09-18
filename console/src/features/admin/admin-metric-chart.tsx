"use client";

import { useEffect, useRef } from "react";
import * as echarts from "echarts/core";
import { type ComposeOption } from "echarts/core";
import {
  DataZoomComponent,
  GridComponent,
  LegendComponent,
  TooltipComponent,
  type DataZoomComponentOption,
  type GridComponentOption,
  type LegendComponentOption,
  type TooltipComponentOption,
} from "echarts/components";
import { LineChart, type LineSeriesOption } from "echarts/charts";
import { CanvasRenderer } from "echarts/renderers";

echarts.use([
  DataZoomComponent,
  GridComponent,
  LegendComponent,
  TooltipComponent,
  LineChart,
  CanvasRenderer,
]);

type MetricChartOption = ComposeOption<
  | LineSeriesOption
  | DataZoomComponentOption
  | GridComponentOption
  | LegendComponentOption
  | TooltipComponentOption
>;

type AdminMetric = {
  timestamp: string;
  name: string;
  service: string;
  value: number;
};

export function AdminMetricChart({ items }: { items: AdminMetric[] }) {
  const chartRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const element = chartRef.current;
    if (!element || !items.length) return;
    const chart = echarts.init(element, undefined, { renderer: "canvas" });
    const grouped = new Map<string, AdminMetric[]>();
    for (const item of items) {
      const key = `${item.service} · ${item.name}`;
      const group = grouped.get(key) ?? [];
      group.push(item);
      grouped.set(key, group);
    }
    const series = Array.from(grouped.entries())
      .slice(0, 8)
      .map(([name, points]) => ({
        name,
        type: "line" as const,
        showSymbol: false,
        smooth: false,
        connectNulls: false,
        data: points
          .slice()
          .sort(
            (left, right) =>
              Date.parse(left.timestamp) - Date.parse(right.timestamp),
          )
          .map((point) => [point.timestamp, point.value]),
      }));
    const option: MetricChartOption = {
      animation: false,
      backgroundColor: "transparent",
      color: ["#e4f222", "#02b8cc", "#8b5cf6", "#27a644", "#eb5757"],
      grid: { top: 32, right: 20, bottom: 64, left: 64 },
      legend: {
        top: 0,
        left: 0,
        type: "scroll",
        textStyle: { color: "#8a8f98", fontSize: 11 },
      },
      tooltip: {
        trigger: "axis",
        backgroundColor: "#161718",
        borderColor: "#383b3f",
        textStyle: { color: "#d0d6e0", fontSize: 12 },
      },
      xAxis: {
        type: "time",
        axisLabel: { color: "#62666d", fontSize: 11 },
        axisLine: { lineStyle: { color: "#23252a" } },
        splitLine: { lineStyle: { color: "#23252a" } },
      },
      yAxis: {
        type: "value",
        scale: true,
        axisLabel: { color: "#62666d", fontSize: 11 },
        axisLine: { show: false },
        splitLine: { lineStyle: { color: "#23252a" } },
      },
      dataZoom: [
        { type: "inside", filterMode: "none" },
        {
          type: "slider",
          height: 16,
          bottom: 8,
          borderColor: "#23252a",
          backgroundColor: "#0f1011",
          fillerColor: "rgba(228, 242, 34, 0.12)",
          handleStyle: { color: "#e4f222" },
          textStyle: { color: "#62666d", fontSize: 10 },
        },
      ],
      series,
    };
    chart.setOption(option);
    const observer = new ResizeObserver(() => chart.resize());
    observer.observe(element);
    return () => {
      observer.disconnect();
      chart.dispose();
    };
  }, [items]);

  return (
    <div
      ref={chartRef}
      className="h-72 w-full"
      aria-label="Metric time series"
    />
  );
}
