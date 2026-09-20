"use client";

import { useEffect, useId, useRef } from "react";
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
import { formatDate } from "@/lib/format";

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
  const descriptionId = useId();

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

  const totalSeriesCount = new Set(
    items.map((item) => `${item.service} · ${item.name}`),
  ).size;
  const seriesCount = Math.min(totalSeriesCount, 8);
  const seriesLabel =
    totalSeriesCount > seriesCount
      ? `Metric time series showing ${seriesCount} of ${totalSeriesCount} series`
      : `Metric time series with ${seriesCount} series`;

  return (
    <div className="space-y-3">
      <div
        ref={chartRef}
        className="h-72 w-full"
        role="img"
        aria-label={seriesLabel}
        aria-describedby={descriptionId}
      />
      <p id={descriptionId} className="sr-only">
        {items.length} metric points across {totalSeriesCount} series are
        available. The chart shows up to eight series. Expand the data table to
        inspect the timestamps, metric names, services, and values.
      </p>
      <details className="rounded-md border border-graphite bg-void">
        <summary className="flex min-h-11 cursor-pointer items-center px-3 text-xs font-medium text-mist hover:text-paper">
          View data table
        </summary>
        <div className="overflow-x-auto border-t border-graphite p-3">
          <table className="w-full min-w-[640px] text-left text-xs">
            <caption className="sr-only">Metric time series data</caption>
            <thead className="text-fog">
              <tr>
                <th className="px-2 py-2 font-medium">Time</th>
                <th className="px-2 py-2 font-medium">Metric</th>
                <th className="px-2 py-2 font-medium">Service</th>
                <th className="px-2 py-2 text-right font-medium">Value</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-graphite">
              {items.map((item, index) => (
                <tr
                  key={`${item.timestamp}-${item.name}-${item.service}-${index}`}
                >
                  <td className="whitespace-nowrap px-2 py-2 font-mono text-mist">
                    <time dateTime={item.timestamp}>
                      {formatDate(item.timestamp)}
                    </time>
                  </td>
                  <td className="px-2 py-2 font-mono text-mist">{item.name}</td>
                  <td className="px-2 py-2 text-mist">{item.service}</td>
                  <td className="px-2 py-2 text-right font-mono tabular-nums text-mist">
                    {item.value.toLocaleString(undefined, {
                      maximumFractionDigits: 4,
                    })}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </div>
  );
}
