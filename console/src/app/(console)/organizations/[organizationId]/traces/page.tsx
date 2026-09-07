import { OrganizationTracesView } from "@/features/organization/organization-views";

export default async function OrganizationTracesPage({
  params,
}: {
  params: Promise<{ organizationId: string }>;
}) {
  const { organizationId } = await params;
  return <OrganizationTracesView organizationId={organizationId} />;
}
