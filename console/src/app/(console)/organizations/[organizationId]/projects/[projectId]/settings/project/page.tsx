import { ProjectSettingsView } from "@/features/resources/misc-views";

export default async function ProjectSettingsPage({ params }: { params: Promise<{ projectId: string }> }) { const { projectId } = await params; return <ProjectSettingsView projectId={projectId} />; }
