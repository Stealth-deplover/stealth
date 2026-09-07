import { OrganizationOverviewView } from "@/features/organization/organization-views";

export default async function OrganizationPage({
  params,
}: {
  params: Promise<{ organizationId: string }>;
}) {
  const { organizationId } = await params;
  return <OrganizationOverviewView organizationId={organizationId} />;
}
