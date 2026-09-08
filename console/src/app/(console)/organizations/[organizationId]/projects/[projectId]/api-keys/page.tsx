import { APIKeysView } from "@/features/resources/collection-views";

export default async function APIKeysPage({
  params,
}: {
  params: Promise<{ organizationId: string; projectId: string }>;
}) {
  const { organizationId, projectId } = await params;
  return <APIKeysView organizationId={organizationId} projectId={projectId} />;
}
