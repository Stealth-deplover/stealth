import { AgentRunsView } from "@/features/resources/detail-views";

export default async function AgentRunsPage({ params }: { params: Promise<{ organizationId: string; projectId: string; agentId: string }> }) { const { organizationId, projectId, agentId } = await params; return <AgentRunsView organizationId={organizationId} projectId={projectId} agentId={agentId} />; }
