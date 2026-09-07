import { MessagingView } from "@/features/resources/misc-views";

export default async function MessagingPage({
  params,
}: {
  params: Promise<{ projectId: string }>;
}) {
  const { projectId } = await params;
  return <MessagingView projectId={projectId} />;
}
