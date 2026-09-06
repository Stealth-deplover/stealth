import { DatabasesView } from "@/features/resources/collection-views";

export default async function DatabasesPage({ params }: { params: Promise<{ organizationId: string; projectId: string }> }) { const { organizationId, projectId } = await params; return <DatabasesView organizationId={organizationId} projectId={projectId} />; }
