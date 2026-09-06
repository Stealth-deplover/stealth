import { OrganizationAuditView } from "@/features/organization/organization-views";

export default async function AuditPage({ params }: { params: Promise<{ organizationId: string }> }) {
  const { organizationId } = await params;
  return <OrganizationAuditView organizationId={organizationId} />;
}
