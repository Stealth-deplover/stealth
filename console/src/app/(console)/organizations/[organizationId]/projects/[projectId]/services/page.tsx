import { ServicesCanvasLoader } from "@/features/services/services-canvas-loader";

export default async function ServicesPage({ params }: { params: Promise<{ projectId: string }> }) { const { projectId } = await params; return <ServicesCanvasLoader projectId={projectId} />; }
