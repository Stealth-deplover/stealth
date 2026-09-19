"use client";

import { useEffect, useRef } from "react";
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

echarts.use([BarChart, GridComponent, TooltipComponent, CanvasRenderer]);

type VolumeChartOption = ComposeOption<
  BarSeriesOption | GridComponentOption | TooltipComponentOption
>;

type VolumeBucket = { timestamp: string; count: number };

export function AdminLogVolumeChart({ items }: { items: VolumeBucket[] }) {
  const chartRef = useRef<HTMLDivElement>(null);

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

  return <div ref={chartRef} className="h-48 w-full" aria-label="Log volume" />;
}
