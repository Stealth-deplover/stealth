import { ServicesCanvasLoader } from "@/features/services/services-canvas-loader";

export default async function ServicesPage({ params }: { params: Promise<{ organizationId: string; projectId: string }> }) { const { organizationId, projectId } = await params; return <ServicesCanvasLoader organizationId={organizationId} projectId={projectId} />; }
