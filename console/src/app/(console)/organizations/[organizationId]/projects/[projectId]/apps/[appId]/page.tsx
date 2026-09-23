import { AppDetailView } from "@/features/apps/app-detail-view";

export default async function AppPage({
  params,
}: {
  params: Promise<{ organizationId: string; projectId: string; appId: string }>;
}) {
  const { organizationId, projectId, appId } = await params;
  return (
    <AppDetailView
      organizationId={organizationId}
      projectId={projectId}
      appId={appId}
    />
  );
}
