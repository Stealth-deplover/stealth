import { ProjectUserDetailView } from "@/features/users/project-user-detail-view";

export default async function ProjectUserPage({
  params,
}: {
  params: Promise<{
    organizationId: string;
    projectId: string;
    userId: string;
  }>;
}) {
  const { organizationId, projectId, userId } = await params;
  return (
    <ProjectUserDetailView
      organizationId={organizationId}
      projectId={projectId}
      userId={userId}
    />
  );
}
