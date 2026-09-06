import { AuthSettingsView } from "@/features/resources/misc-views";

export default async function AuthPage({ params }: { params: Promise<{ projectId: string }> }) { const { projectId } = await params; return <AuthSettingsView projectId={projectId} />; }
