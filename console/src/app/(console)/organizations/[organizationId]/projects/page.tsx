import { OrganizationProjectsView } from "@/features/organization/organization-views";

export default async function OrganizationProjectsPage({ params }: { params: Promise<{ organizationId: string }> }) {
  const { organizationId } = await params;
  return <OrganizationProjectsView organizationId={organizationId} />;
}
