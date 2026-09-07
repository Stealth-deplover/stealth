"use client";

import "@xyflow/react/dist/style.css";
import { Background, Controls, MiniMap, Panel, ReactFlow, Handle, Position, useEdgesState, useNodesState, type Node, type NodeProps } from "@xyflow/react";
import { Database, FileArchive, FunctionSquare, HardDrive, Save, Waypoints } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { useCanvasDatabases, useCanvasFunctions, useCanvasStorageBuckets, useCanvasSites, useServiceLayout } from "@/api/queries";
import { useReplaceServiceLayout } from "@/api/mutations";
import type { components } from "@/api/generated/schema";
import { PageHeader } from "@/components/page-header";
import { ErrorState } from "@/components/feedback/error-state";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { resourceAccent } from "@/lib/constants";

type ResourceNodeData = { label: string; type: "function" | "site" | "database" | "storage"; status?: string; subtitle?: string };
type ResourceNode = Node<ResourceNodeData, "resource">;

function ResourceNode({ data }: NodeProps<ResourceNode>) {
  const Icon = data.type === "function" ? FunctionSquare : data.type === "site" ? FileArchive : data.type === "database" ? Database : HardDrive;
  return <div className={`min-w-48 rounded-xl border px-4 py-3 shadow-xl ${resourceAccent[data.type]}`}><Handle type="target" position={Position.Top} className="!border-2 !border-stealth-bg !bg-slate-400" /><div className="flex items-start gap-3"><span className="mt-0.5 flex size-7 items-center justify-center rounded-lg bg-black/20"><Icon className="size-4" /></span><div className="min-w-0"><p className="truncate text-sm font-semibold">{data.label}</p><p className="mt-0.5 truncate text-[11px] opacity-70">{data.subtitle ?? data.type}</p></div></div>{data.status ? <div className="mt-3"><Badge variant="neutral">{data.status}</Badge></div> : null}<Handle type="source" position={Position.Bottom} className="!border-2 !border-stealth-bg !bg-slate-400" /></div>;
}

const nodeTypes = { resource: ResourceNode };

export function ServicesCanvasView({ projectId }: { projectId: string }) {
  const functions = useCanvasFunctions(projectId);
  const sites = useCanvasSites(projectId);
  const databases = useCanvasDatabases(projectId);
  const buckets = useCanvasStorageBuckets(projectId);
  const layout = useServiceLayout(projectId);
  const save = useReplaceServiceLayout(projectId);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const layoutMap = useMemo(() => new Map((layout.data?.layout ?? []).map((item) => [`${item.resource_type}:${item.resource_id}`, item])), [layout.data?.layout]);
  const initialNodes = useMemo<ResourceNode[]>(() => {
    const nodes: ResourceNode[] = [];
    functions.data?.forEach((item, index) => { const saved = layoutMap.get(`function:${item.id}`); nodes.push({ id: `function:${item.id}`, type: "resource", position: { x: saved?.x ?? 80 + (index % 3) * 250, y: saved?.y ?? 80 + Math.floor(index / 3) * 180 }, data: { label: item.name, type: "function", status: item.status, subtitle: item.runtime } }); });
    sites.data?.forEach((item, index) => { const saved = layoutMap.get(`site:${item.id}`); nodes.push({ id: `site:${item.id}`, type: "resource", position: { x: saved?.x ?? 80 + (index % 3) * 250, y: saved?.y ?? 360 + Math.floor(index / 3) * 180 }, data: { label: item.name, type: "site", status: item.status, subtitle: item.framework } }); });
    databases.data?.forEach((item, index) => { const saved = layoutMap.get(`database:${item.id}`); nodes.push({ id: `database:${item.id}`, type: "resource", position: { x: saved?.x ?? 80 + (index % 3) * 250, y: saved?.y ?? 640 + Math.floor(index / 3) * 180 }, data: { label: item.name, type: "database", subtitle: "typed data" } }); });
    buckets.data?.forEach((item, index) => { const saved = layoutMap.get(`storage:${item.id}`); nodes.push({ id: `storage:${item.id}`, type: "resource", position: { x: saved?.x ?? 80 + (index % 3) * 250, y: saved?.y ?? 920 + Math.floor(index / 3) * 180 }, data: { label: item.name, type: "storage", subtitle: "object storage" } }); });
    return nodes;
  }, [buckets.data, databases.data, functions.data, layoutMap, sites.data]);
  const [nodes, setNodes, onNodesChange] = useNodesState<ResourceNode>(initialNodes);
  const [edges, , onEdgesChange] = useEdgesState([]);
  useEffect(() => { setNodes(initialNodes); }, [initialNodes, setNodes]);
  const saveLayout = () => { save.mutate({ layout: nodes.map((node) => { const [resource_type, resource_id] = node.id.split(":") as [ResourceNodeData["type"], string]; return { resource_type: resource_type as components["schemas"]["ProjectServiceLayoutItemRequest"]["resource_type"], resource_id, x: Math.round(node.position.x), y: Math.round(node.position.y) }; }) }, { onSuccess: () => toast.success("Canvas positions saved") }); };
  const loading = functions.isLoading || sites.isLoading || databases.isLoading || buckets.isLoading || layout.isLoading;
  const error = functions.error ?? sites.error ?? databases.error ?? buckets.error ?? layout.error;
  const selected = selectedId ? nodes.find((node) => node.id === selectedId) : undefined;
  if (error) return <ErrorState error={error} retry={() => { void functions.refetch(); void sites.refetch(); void databases.refetch(); void buckets.refetch(); void layout.refetch(); }} />;
  return <><PageHeader eyebrow="Topology" title="Services" description="A durable map of every resource returned by the cursor-paginated APIs. Relationships are intentionally absent until the backend exposes explicit dependency edges." actions={layout.data?.can_manage ? <Button onClick={saveLayout} disabled={save.isPending || loading}><Save className="size-4" /> Save positions</Button> : null} /><div className="mb-4 flex items-center gap-2"><Badge variant="neutral"><Waypoints className="size-3" /> {nodes.length} nodes</Badge><span className="text-xs text-slate-600">The canvas follows all resource cursors. Drag, zoom, pan, and fit view; no synthetic edges.</span></div><Card className="h-[calc(100vh-15rem)] min-h-[520px] overflow-hidden"><ReactFlow nodes={nodes} edges={edges} nodeTypes={nodeTypes} onNodesChange={onNodesChange} onEdgesChange={onEdgesChange} onNodeClick={(_, node) => setSelectedId(node.id)} onPaneClick={() => setSelectedId(null)} fitView proOptions={{ hideAttribution: true }} className="bg-[#090c10]"><Background color="#27303c" gap={28} size={1} /><Controls /><MiniMap nodeColor={(node) => node.data?.type === "function" ? "#72e4dc" : node.data?.type === "site" ? "#b9a1ff" : node.data?.type === "database" ? "#f2c66d" : "#73d6a2"} /><Panel position="top-right" className="rounded-lg border border-stealth-border bg-stealth-elevated/90 px-3 py-2 text-xs text-slate-500">Resource layout only · backend-owned</Panel>{selected ? <Panel position="bottom-right" className="w-64 rounded-xl border border-stealth-border bg-stealth-elevated/95 p-4 shadow-2xl"><p className="text-[10px] uppercase tracking-[0.16em] text-slate-600">Selected resource</p><p className="mt-2 truncate text-sm font-semibold text-white">{selected.data.label}</p><p className="mt-1 text-xs capitalize text-slate-500">{selected.data.type}{selected.data.subtitle ? ` · ${selected.data.subtitle}` : ""}</p>{selected.data.status ? <div className="mt-3"><Badge variant="neutral">{selected.data.status}</Badge></div> : null}<p className="mt-3 text-[11px] leading-5 text-slate-600">Edges are withheld until the API reports a real dependency.</p></Panel> : null}</ReactFlow></Card></>;
}
