import { FunctionsView } from "@/features/resources/collection-views";

export default async function FunctionsPage({ params }: { params: Promise<{ organizationId: string; projectId: string }> }) { const { organizationId, projectId } = await params; return <FunctionsView organizationId={organizationId} projectId={projectId} />; }
