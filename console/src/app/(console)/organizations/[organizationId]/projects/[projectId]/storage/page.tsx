import { StorageView } from "@/features/resources/collection-views";

export default async function StoragePage({
  params,
}: {
  params: Promise<{ organizationId: string; projectId: string }>;
}) {
  const { organizationId, projectId } = await params;
  return <StorageView organizationId={organizationId} projectId={projectId} />;
}
