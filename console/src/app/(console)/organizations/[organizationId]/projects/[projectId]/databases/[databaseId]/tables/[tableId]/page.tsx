import { DatabaseRowsView } from "@/features/resources/detail-views";

export default async function TablePage({ params }: { params: Promise<{ organizationId: string; projectId: string; databaseId: string; tableId: string }> }) { const { organizationId, projectId, databaseId, tableId } = await params; return <DatabaseRowsView organizationId={organizationId} projectId={projectId} databaseId={databaseId} tableId={tableId} />; }
