import { LogsView } from "@/features/resources/misc-views";

export default async function LogsPage({ params }: { params: Promise<{ organizationId: string; projectId: string }> }) { const { organizationId, projectId } = await params; return <LogsView organizationId={organizationId} projectId={projectId} />; }
