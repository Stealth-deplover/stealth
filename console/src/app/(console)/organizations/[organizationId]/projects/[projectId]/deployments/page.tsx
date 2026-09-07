import { DeploymentsView } from "@/features/resources/misc-views";

export default async function DeploymentsPage({
  params,
}: {
  params: Promise<{ organizationId: string; projectId: string }>;
}) {
  const { organizationId, projectId } = await params;
  return (
    <DeploymentsView organizationId={organizationId} projectId={projectId} />
  );
}
