import { OrganizationIncidentsView } from "@/features/organization/organization-views";

export default async function IncidentsPage({ params }: { params: Promise<{ organizationId: string }> }) {
  const { organizationId } = await params;
  return <OrganizationIncidentsView organizationId={organizationId} />;
}
