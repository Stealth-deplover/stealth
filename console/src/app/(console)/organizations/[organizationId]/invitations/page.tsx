import { OrganizationMembersView } from "@/features/organization/organization-views";

export default async function InvitationsPage({
  params,
}: {
  params: Promise<{ organizationId: string }>;
}) {
  const { organizationId } = await params;
  return <OrganizationMembersView organizationId={organizationId} />;
}
