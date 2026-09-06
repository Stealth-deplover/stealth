import { SiteDeploymentView } from "@/features/resources/detail-views";

export default async function SiteDeploymentPage({ params }: { params: Promise<{ organizationId: string; projectId: string; siteId: string; deploymentId: string }> }) {
  const { organizationId, projectId, siteId, deploymentId } = await params;
  return <SiteDeploymentView organizationId={organizationId} projectId={projectId} siteId={siteId} deploymentId={deploymentId} />;
}
