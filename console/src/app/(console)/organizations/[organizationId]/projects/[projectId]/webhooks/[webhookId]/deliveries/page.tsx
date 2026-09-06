import { WebhookDetailView } from "@/features/resources/detail-views";

export default async function WebhookDeliveriesPage({ params }: { params: Promise<{ organizationId: string; projectId: string; webhookId: string }> }) { const { organizationId, projectId, webhookId } = await params; return <WebhookDetailView organizationId={organizationId} projectId={projectId} webhookId={webhookId} />; }
