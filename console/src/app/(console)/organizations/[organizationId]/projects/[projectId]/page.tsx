import { ProjectOverviewView } from "@/features/project/project-overview";

export default async function ProjectPage({
  params,
}: {
  params: Promise<{ organizationId: string; projectId: string }>;
}) {
  const { organizationId, projectId } = await params;
  return (
    <ProjectOverviewView
      organizationId={organizationId}
      projectId={projectId}
    />
  );
}
