import { AgentsView } from "@/features/resources/misc-views";

export default async function AgentsPage({
  params,
}: {
  params: Promise<{ organizationId: string; projectId: string }>;
}) {
  const { organizationId, projectId } = await params;
  return <AgentsView organizationId={organizationId} projectId={projectId} />;
}
