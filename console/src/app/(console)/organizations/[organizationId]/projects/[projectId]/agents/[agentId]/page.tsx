import { AgentDetailView } from "@/features/resources/misc-views";

export default async function AgentPage({
  params,
}: {
  params: Promise<{
    organizationId: string;
    projectId: string;
    agentId: string;
  }>;
}) {
  const { organizationId, projectId, agentId } = await params;
  return (
    <AgentDetailView
      organizationId={organizationId}
      projectId={projectId}
      agentId={agentId}
    />
  );
}
