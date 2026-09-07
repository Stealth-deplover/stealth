"use client";

import "@xyflow/react/dist/style.css";
import {
  Background,
  Controls,
  MiniMap,
  Panel,
  ReactFlow,
  useEdgesState,
  useNodesState,
  type Node,
  type NodeProps,
} from "@xyflow/react";
import {
  ArrowUpRight,
  Database,
  FileArchive,
  FunctionSquare,
  HardDrive,
  Save,
  Waypoints,
  type LucideIcon,
} from "lucide-react";
import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import {
  useCanvasDatabases,
  useCanvasFunctions,
  useCanvasStorageBuckets,
  useCanvasSites,
  useServiceLayout,
} from "@/api/queries";
import { useReplaceServiceLayout } from "@/api/mutations";
import type { components } from "@/api/generated/schema";
import { PageHeader } from "@/components/page-header";
import { ErrorState } from "@/components/feedback/error-state";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { EmptyState } from "@/components/empty-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { resourceAccent } from "@/lib/constants";

type ResourceNodeData = {
  label: string;
  type: "function" | "site" | "database" | "storage";
  status?: string;
  subtitle?: string;
};
type ResourceNode = Node<ResourceNodeData, "resource">;

const RESOURCE_ICONS: Record<ResourceNodeData["type"], LucideIcon> = {
  function: FunctionSquare,
  site: FileArchive,
  database: Database,
  storage: HardDrive,
};

const RESOURCE_MINIMAP_COLORS: Record<ResourceNodeData["type"], string> = {
  function: "#72e4dc",
  site: "#b9a1ff",
  database: "#f2c66d",
  storage: "#73d6a2",
};

const RESOURCE_PATHS: Record<ResourceNodeData["type"], string> = {
  function: "functions",
  site: "sites",
  database: "databases",
  storage: "storage",
};

function getResourceLink(
  base: string,
  resourceType: string,
  resourceId: string,
) {
  const path = RESOURCE_PATHS[resourceType as ResourceNodeData["type"]];
  return path ? `${base}/${path}/${resourceId}` : undefined;
}

function getMiniMapColor(type: unknown) {
  return typeof type === "string" && type in RESOURCE_MINIMAP_COLORS
    ? RESOURCE_MINIMAP_COLORS[type as ResourceNodeData["type"]]
    : RESOURCE_MINIMAP_COLORS.storage;
}

function ResourceNode({ data }: NodeProps<ResourceNode>) {
  const Icon = RESOURCE_ICONS[data.type];
  return (
    <div
      className={`min-w-48 rounded-xl border px-4 py-3 shadow-xl ${resourceAccent[data.type]}`}
    >
      <div className="flex items-start gap-3">
        <span className="mt-0.5 flex size-7 items-center justify-center rounded-lg bg-black/20">
          <Icon className="size-4" />
        </span>
        <div className="min-w-0">
          <p className="truncate text-sm font-semibold">{data.label}</p>
          <p className="mt-0.5 truncate text-[11px] opacity-70">
            {data.subtitle ?? data.type}
          </p>
        </div>
      </div>
      {data.status ? (
        <div className="mt-3">
          <StatusBadge status={data.status} />
        </div>
      ) : null}
    </div>
  );
}

const nodeTypes = { resource: ResourceNode };

export function ServicesCanvasView({
  organizationId,
  projectId,
}: {
  organizationId: string;
  projectId: string;
}) {
  const functions = useCanvasFunctions(projectId);
  const sites = useCanvasSites(projectId);
  const databases = useCanvasDatabases(projectId);
  const buckets = useCanvasStorageBuckets(projectId);
  const layout = useServiceLayout(projectId);
  const save = useReplaceServiceLayout(projectId);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const layoutMap = useMemo(
    () =>
      new Map(
        (layout.data?.layout ?? []).map((item) => [
          `${item.resource_type}:${item.resource_id}`,
          item,
        ]),
      ),
    [layout.data?.layout],
  );
  const initialNodes = useMemo<ResourceNode[]>(() => {
    const nodes: ResourceNode[] = [];
    functions.data?.forEach((item, index) => {
      const saved = layoutMap.get(`function:${item.id}`);
      nodes.push({
        id: `function:${item.id}`,
        type: "resource",
        position: {
          x: saved?.x ?? 80 + (index % 3) * 250,
          y: saved?.y ?? 80 + Math.floor(index / 3) * 180,
        },
        data: {
          label: item.name,
          type: "function",
          status: item.status,
          subtitle: item.runtime,
        },
      });
    });
    sites.data?.forEach((item, index) => {
      const saved = layoutMap.get(`site:${item.id}`);
      nodes.push({
        id: `site:${item.id}`,
        type: "resource",
        position: {
          x: saved?.x ?? 80 + (index % 3) * 250,
          y: saved?.y ?? 360 + Math.floor(index / 3) * 180,
        },
        data: {
          label: item.name,
          type: "site",
          status: item.status,
          subtitle: item.framework,
        },
      });
    });
    databases.data?.forEach((item, index) => {
      const saved = layoutMap.get(`database:${item.id}`);
      nodes.push({
        id: `database:${item.id}`,
        type: "resource",
        position: {
          x: saved?.x ?? 80 + (index % 3) * 250,
          y: saved?.y ?? 640 + Math.floor(index / 3) * 180,
        },
        data: { label: item.name, type: "database", subtitle: "typed data" },
      });
    });
    buckets.data?.forEach((item, index) => {
      const saved = layoutMap.get(`storage:${item.id}`);
      nodes.push({
        id: `storage:${item.id}`,
        type: "resource",
        position: {
          x: saved?.x ?? 80 + (index % 3) * 250,
          y: saved?.y ?? 920 + Math.floor(index / 3) * 180,
        },
        data: { label: item.name, type: "storage", subtitle: "object storage" },
      });
    });
    return nodes;
  }, [buckets.data, databases.data, functions.data, layoutMap, sites.data]);
  const [nodes, setNodes, onNodesChange] =
    useNodesState<ResourceNode>(initialNodes);
  const [edges, , onEdgesChange] = useEdgesState([]);
  useEffect(() => {
    setNodes(initialNodes);
  }, [initialNodes, setNodes]);
  const saveLayout = () => {
    save.mutate(
      {
        layout: nodes.map((node) => {
          const [resource_type, resource_id] = node.id.split(":") as [
            ResourceNodeData["type"],
            string,
          ];
          return {
            resource_type:
              resource_type as components["schemas"]["ProjectServiceLayoutItemRequest"]["resource_type"],
            resource_id,
            x: Math.round(node.position.x),
            y: Math.round(node.position.y),
          };
        }),
      },
      { onSuccess: () => toast.success("Canvas positions saved") },
    );
  };
  const loading =
    functions.isLoading ||
    sites.isLoading ||
    databases.isLoading ||
    buckets.isLoading ||
    layout.isLoading;
  const error =
    functions.error ??
    sites.error ??
    databases.error ??
    buckets.error ??
    layout.error;
  const selected = selectedId
    ? nodes.find((node) => node.id === selectedId)
    : undefined;
  const resourceLink = selected
    ? getResourceLink(
        `/organizations/${organizationId}/projects/${projectId}`,
        selected.id.split(":")[0],
        selected.id.split(":")[1],
      )
    : undefined;
  if (error)
    return (
      <ErrorState
        error={error}
        retry={() => {
          void functions.refetch();
          void sites.refetch();
          void databases.refetch();
          void buckets.refetch();
          void layout.refetch();
        }}
      />
    );
  return (
    <>
      <PageHeader
        eyebrow="Topology"
        title="Services"
        description="Map project resources with persisted positions. Dependency edges appear only when the API provides them."
        actions={
          layout.data?.can_manage ? (
            <Button onClick={saveLayout} disabled={save.isPending || loading}>
              <Save className="size-4" /> Save positions
            </Button>
          ) : null
        }
      />
      <div className="mb-4 flex flex-wrap items-center gap-2">
        <Badge variant="neutral">
          <Waypoints className="size-3" /> {nodes.length} nodes
        </Badge>
        <span className="text-xs text-slate-600">
          Resource layout only. Drag, zoom, pan, and fit view; no synthetic
          edges.
        </span>
      </div>
      {loading ? (
        <Card className="flex min-h-[420px] items-center justify-center">
          <LoadingState rows={3} />
        </Card>
      ) : nodes.length === 0 ? (
        <EmptyState
          icon={<Waypoints className="size-5" />}
          title="No services yet"
          description="Create a Function, Site, Database, or Storage resource and it will appear here. The canvas only renders resources returned by the API."
        />
      ) : (
        <Card className="h-[calc(100vh-15rem)] min-h-[460px] overflow-hidden">
          <ReactFlow
            nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onNodeClick={(_, node) => setSelectedId(node.id)}
            onPaneClick={() => setSelectedId(null)}
            fitView
            proOptions={{ hideAttribution: true }}
            className="bg-stealth-bg"
          >
            <Background color="var(--border)" gap={28} size={1} />
            <Controls />
            <MiniMap nodeColor={(node) => getMiniMapColor(node.data?.type)} />
            <Panel
              position="top-right"
              className="rounded-lg border border-stealth-border bg-stealth-elevated/90 px-3 py-2 text-xs text-slate-500"
            >
              Resource layout only · backend-owned
            </Panel>
            {selected ? (
              <Panel
                position="bottom-right"
                className="w-64 rounded-xl border border-stealth-border bg-stealth-elevated/95 p-4 shadow-2xl"
              >
                <p className="text-[10px] uppercase tracking-[0.16em] text-slate-600">
                  Selected resource
                </p>
                <p className="mt-2 truncate text-sm font-semibold text-white">
                  {selected.data.label}
                </p>
                <p className="mt-1 text-xs capitalize text-slate-500">
                  {selected.data.type}
                  {selected.data.subtitle ? ` · ${selected.data.subtitle}` : ""}
                </p>
                {selected.data.status ? (
                  <div className="mt-3">
                    <StatusBadge status={selected.data.status} />
                  </div>
                ) : null}
                <p className="mt-3 text-[11px] leading-5 text-slate-600">
                  Edges are withheld until the API reports a real dependency.
                </p>
                {resourceLink ? (
                  <Button
                    asChild
                    variant="ghost"
                    size="sm"
                    className="mt-3 w-full justify-between"
                  >
                    <Link href={resourceLink}>
                      Open resource <ArrowUpRight className="size-3.5" />
                    </Link>
                  </Button>
                ) : null}
              </Panel>
            ) : null}
          </ReactFlow>
        </Card>
      )}
    </>
  );
}
