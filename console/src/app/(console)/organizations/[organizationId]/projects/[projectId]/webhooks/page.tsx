import { WebhooksView } from "@/features/resources/collection-views";

export default async function WebhooksPage({
  params,
}: {
  params: Promise<{ organizationId: string; projectId: string }>;
}) {
  const { organizationId, projectId } = await params;
  return <WebhooksView organizationId={organizationId} projectId={projectId} />;
}
