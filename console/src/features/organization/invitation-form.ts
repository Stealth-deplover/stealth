import { CreateOrganizationInvitationRequestRole } from "@/api/generated/schema";
import type { components } from "@/api/generated/schema";
import type { CreateField } from "@/components/create-dialog";

export type OrganizationInvitationFormValues = {
  email: string;
  role: string;
};

export const organizationInvitationFields: readonly CreateField<OrganizationInvitationFormValues>[] =
  [
    { name: "email", label: "Email", type: "email" },
    {
      name: "role",
      label: "Role",
      type: "select",
      defaultValue: "developer",
      options: Object.values(CreateOrganizationInvitationRequestRole).map(
        (role) => ({
          value: role,
          label: role[0].toUpperCase() + role.slice(1),
        }),
      ),
    },
  ];

function isInvitationRole(
  value: string,
): value is CreateOrganizationInvitationRequestRole {
  return Object.values(CreateOrganizationInvitationRequestRole).includes(
    value as CreateOrganizationInvitationRequestRole,
  );
}

export function organizationInvitationPayload(
  values: OrganizationInvitationFormValues,
): components["schemas"]["CreateOrganizationInvitationRequest"] {
  const email = values.email.trim();
  if (!email) throw new Error("Email is required.");
  if (!isInvitationRole(values.role)) {
    throw new Error("Role is not supported by the current Stealth API.");
  }
  return { email, role: values.role };
}
