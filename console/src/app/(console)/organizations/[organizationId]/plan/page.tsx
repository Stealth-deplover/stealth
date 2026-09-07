import { OrganizationPlanView } from "@/features/organization/organization-views";

export default async function PlanPage({
  params,
}: {
  params: Promise<{ organizationId: string }>;
}) {
  const { organizationId } = await params;
  return <OrganizationPlanView organizationId={organizationId} />;
}
