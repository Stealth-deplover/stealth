import { FunctionDetailView } from "@/features/resources/detail-views";

export default async function FunctionPage({ params }: { params: Promise<{ organizationId: string; projectId: string; functionId: string }> }) { const { organizationId, projectId, functionId } = await params; return <FunctionDetailView organizationId={organizationId} projectId={projectId} functionId={functionId} />; }
