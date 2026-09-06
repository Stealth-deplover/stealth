import { ServicesCanvasView } from "@/features/services/services-canvas-view";

export default async function ServicesPage({ params }: { params: Promise<{ projectId: string }> }) { const { projectId } = await params; return <ServicesCanvasView projectId={projectId} />; }
