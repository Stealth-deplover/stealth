import { AppsView } from "@/features/apps/apps-view";

export default async function AppsPage({
  params,
}: {
  params: Promise<{ organizationId: string; projectId: string }>;
}) {
  const { organizationId, projectId } = await params;
  return <AppsView organizationId={organizationId} projectId={projectId} />;
}
