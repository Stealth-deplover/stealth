"use client";

import { useEffect, useId, useRef } from "react";
import * as echarts from "echarts/core";
import { BarChart, type BarSeriesOption } from "echarts/charts";
import {
  GridComponent,
  TooltipComponent,
  type GridComponentOption,
  type TooltipComponentOption,
} from "echarts/components";
import { type ComposeOption } from "echarts/core";
import { CanvasRenderer } from "echarts/renderers";
import { formatDate } from "@/lib/format";

echarts.use([BarChart, GridComponent, TooltipComponent, CanvasRenderer]);

type VolumeChartOption = ComposeOption<
  BarSeriesOption | GridComponentOption | TooltipComponentOption
>;

type VolumeBucket = { timestamp: string; count: number };

export function AdminLogVolumeChart({ items }: { items: VolumeBucket[] }) {
  const chartRef = useRef<HTMLDivElement>(null);
  const descriptionId = useId();

  useEffect(() => {
    const element = chartRef.current;
    if (!element || !items.length) return;
    const chart = echarts.init(element, undefined, { renderer: "canvas" });
    const option: VolumeChartOption = {
      animation: false,
      backgroundColor: "transparent",
      grid: { top: 12, right: 16, bottom: 28, left: 48 },
      tooltip: {
        trigger: "axis",
        backgroundColor: "#161718",
        borderColor: "#383b3f",
        textStyle: { color: "#d0d6e0", fontSize: 12 },
      },
      xAxis: {
        type: "time",
        axisLabel: { color: "#62666d", fontSize: 10 },
        axisLine: { lineStyle: { color: "#23252a" } },
        splitLine: { show: false },
      },
      yAxis: {
        type: "value",
        minInterval: 1,
        axisLabel: { color: "#62666d", fontSize: 10 },
        axisLine: { show: false },
        splitLine: { lineStyle: { color: "#23252a" } },
      },
      series: [
        {
          type: "bar",
          name: "Logs",
          barMaxWidth: 18,
          itemStyle: { color: "#e4f222", opacity: 0.78 },
          data: items.map((item) => [item.timestamp, item.count]),
        },
      ],
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
    <div className="space-y-3">
      <div
        ref={chartRef}
        className="h-48 w-full"
        role="img"
        aria-label="Log volume by time bucket"
        aria-describedby={descriptionId}
      />
      <p id={descriptionId} className="sr-only">
        {items.length} log volume buckets are available. Expand the data table
        to inspect the timestamps and counts.
      </p>
      <details className="rounded-md border border-graphite bg-void">
        <summary className="flex min-h-11 cursor-pointer items-center px-3 text-xs font-medium text-mist hover:text-paper">
          View data table
        </summary>
        <div className="overflow-x-auto border-t border-graphite p-3">
          <table className="w-full min-w-[360px] text-left text-xs">
            <caption className="sr-only">Log volume data</caption>
            <thead className="text-fog">
              <tr>
                <th className="px-2 py-2 font-medium">Time</th>
                <th className="px-2 py-2 text-right font-medium">Logs</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-graphite">
              {items.map((item) => (
                <tr key={`${item.timestamp}-${item.count}`}>
                  <td className="px-2 py-2 font-mono text-mist">
                    <time dateTime={item.timestamp}>
                      {formatDate(item.timestamp)}
                    </time>
                  </td>
                  <td className="px-2 py-2 text-right font-mono tabular-nums text-mist">
                    {item.count.toLocaleString()}
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
