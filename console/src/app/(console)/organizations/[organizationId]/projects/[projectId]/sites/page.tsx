import { SitesView } from "@/features/resources/collection-views";

export default async function SitesPage({ params }: { params: Promise<{ organizationId: string; projectId: string }> }) { const { organizationId, projectId } = await params; return <SitesView organizationId={organizationId} projectId={projectId} />; }
