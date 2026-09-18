"use client";

import "@xyflow/react/dist/style.css";
import {
  Controls,
  Handle,
  Position,
  ReactFlow,
  type Node,
  type NodeProps,
} from "@xyflow/react";
import { Network } from "lucide-react";
import { useMemo } from "react";
import { useAdminServiceMap } from "@/api/queries";
import type { components } from "@/api/generated/schema";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { formatDuration } from "@/lib/format";
import { AdminShell } from "./admin-shell";
import { AdminTimeRange, useAdminTimeRange } from "./admin-time-range";

type ServiceEdge = components["schemas"]["AdminServiceMapEdge"];
type ServiceNodeData = {
  label: string;
  requestRate: number;
  errorRate: number;
  p95: number;
};
type ServiceNode = Node<ServiceNodeData, "service">;

function ServiceNodeView({ data }: NodeProps<ServiceNode>) {
  return (
    <div className="relative min-w-48 rounded-xl border border-graphite bg-carbon px-4 py-3 shadow-xl">
      <Handle
        type="target"
        position={Position.Left}
        className="!border-graphite !bg-smoke"
      />
      <p className="truncate text-sm text-paper">{data.label}</p>
      <div className="mt-3 grid grid-cols-3 gap-3 text-[11px]">
        <Metric label="Requests / sec" value={data.requestRate.toFixed(2)} />
        <Metric
          label="Errors"
          value={`${(data.errorRate * 100).toFixed(1)}%`}
        />
        <Metric label="P95" value={formatDuration(data.p95)} />
      </div>
      <Handle
        type="source"
        position={Position.Right}
        className="!border-graphite !bg-smoke"
      />
    </div>
  );
}

const nodeTypes = { service: ServiceNodeView };

export function AdminServiceMapView() {
  const timeRange = useAdminTimeRange();
  const serviceMap = useAdminServiceMap(
    { ...timeRange.query, limit: 100 },
    { refetchInterval: timeRange.refreshInterval },
  );
  const rangeSeconds = Math.max(
    1,
    (Date.parse(timeRange.query.to) - Date.parse(timeRange.query.from)) / 1000,
  );
  const { nodes, edges } = useMemo(
    () => buildGraph(serviceMap.data?.items ?? [], rangeSeconds),
    [rangeSeconds, serviceMap.data?.items],
  );

  return (
    <AdminShell>
      <PageHeader
        eyebrow="Admin / Telemetry"
        title="Service map"
        description="A topology derived from peer-service attributes in real traces. Services without observed dependencies remain out of this graph."
        actions={
          <AdminTimeRange
            rangeKey={timeRange.rangeKey}
            refreshKey={timeRange.refreshKey}
            onRangeChange={timeRange.setRange}
            onRefreshChange={timeRange.setRefresh}
          />
        }
      />
      {serviceMap.isPending ? <LoadingState rows={4} /> : null}
      {serviceMap.error ? (
        <ErrorState
          title="Could not load service map"
          error={serviceMap.error}
          retry={() => serviceMap.refetch()}
        />
      ) : null}
      {serviceMap.data && !serviceMap.data.items.length ? (
        <Card>
          <CardContent className="flex min-h-80 flex-col items-center justify-center gap-3 text-center">
            <Network className="size-5 text-fog" aria-hidden="true" />
            <p className="text-sm text-paper">
              No trace dependency edges in this window.
            </p>
            <p className="max-w-md text-sm leading-6 text-fog">
              The map needs spans with a peer.service or server.address
              attribute. It does not invent connections from the service list.
            </p>
          </CardContent>
        </Card>
      ) : null}
      {nodes.length ? (
        <Card className="h-[calc(100vh-15rem)] min-h-[520px] overflow-hidden">
          <ReactFlow
            nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            fitView
            proOptions={{ hideAttribution: true }}
            className="bg-void"
          >
            <Controls />
          </ReactFlow>
        </Card>
      ) : null}
      {serviceMap.data?.items.length ? (
        <div className="mt-4 flex flex-wrap items-center gap-2 text-xs text-fog">
          <Badge variant="neutral">{nodes.length} services</Badge>
          <Badge variant="neutral">{edges.length} dependencies</Badge>
          <span>
            Metrics are aggregated by the telemetry backend for the selected
            window.
          </span>
        </div>
      ) : null}
    </AdminShell>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <p className="truncate text-[10px] uppercase tracking-[0.08em] text-fog">
        {label}
      </p>
      <p className="mt-1 truncate font-mono text-xs tabular-nums text-mist">
        {value}
      </p>
    </div>
  );
}

function buildGraph(items: ServiceEdge[], rangeSeconds: number) {
  const nodesByName = new Map<string, ServiceNodeData>();
  for (const item of items) {
    const source = nodesByName.get(item.source) ?? {
      label: item.source,
      requestRate: 0,
      errorRate: 0,
      p95: 0,
    };
    source.requestRate += item.request_count / rangeSeconds;
    source.errorRate = Math.max(source.errorRate, item.error_rate);
    source.p95 = Math.max(source.p95, item.p95_latency_ms);
    nodesByName.set(item.source, source);
    const target = nodesByName.get(item.target) ?? {
      label: item.target,
      requestRate: 0,
      errorRate: 0,
      p95: 0,
    };
    nodesByName.set(item.target, target);
  }
  const nodes: ServiceNode[] = Array.from(nodesByName.entries()).map(
    ([name, data], index) => ({
      id: `service:${name}`,
      type: "service",
      position: { x: (index % 3) * 280, y: Math.floor(index / 3) * 180 },
      data,
    }),
  );
  const edges = items.map((item, index) => ({
    id: `edge:${item.source}:${item.target}:${index}`,
    source: `service:${item.source}`,
    target: `service:${item.target}`,
    label: `${(item.request_count / rangeSeconds).toFixed(2)} req/s · ${(item.error_rate * 100).toFixed(1)}% errors`,
    animated: false,
    style: {
      stroke: item.error_rate > 0 ? "#eb5757" : "#62666d",
      strokeWidth: 1.5,
    },
    labelStyle: { fill: "#8a8f98", fontSize: 10 },
    labelBgStyle: { fill: "#0f1011", fillOpacity: 0.92, color: "#23252a" },
  }));
  return { nodes, edges };
}
