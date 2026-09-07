import { APIKeysView } from "@/features/resources/collection-views";

export default async function APIKeysPage({
  params,
}: {
  params: Promise<{ projectId: string }>;
}) {
  const { projectId } = await params;
  return <APIKeysView projectId={projectId} />;
}
