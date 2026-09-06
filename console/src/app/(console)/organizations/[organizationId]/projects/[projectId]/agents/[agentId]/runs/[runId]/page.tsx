import { AgentRunDetailView } from "@/features/resources/detail-views";

export default async function AgentRunPage({ params }: { params: Promise<{ organizationId: string; projectId: string; agentId: string; runId: string }> }) {
  const { organizationId, projectId, agentId, runId } = await params;
  return <AgentRunDetailView organizationId={organizationId} projectId={projectId} agentId={agentId} runId={runId} />;
}
