import { UsersView } from "@/features/resources/collection-views";

export default async function UsersPage({
  params,
}: {
  params: Promise<{ projectId: string }>;
}) {
  const { projectId } = await params;
  return <UsersView projectId={projectId} />;
}
