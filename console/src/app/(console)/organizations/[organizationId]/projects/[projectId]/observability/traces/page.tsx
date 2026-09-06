import { TracesView } from "@/features/resources/misc-views";

export default async function TracesPage({ params }: { params: Promise<{ projectId: string }> }) { const { projectId } = await params; return <TracesView projectId={projectId} />; }
