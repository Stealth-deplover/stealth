import { UpdateOrganizationMembershipRequestRole } from "@/api/generated/schema";
import type { components } from "@/api/generated/schema";
import type { CreateField } from "@/components/create-dialog";

export type MembershipRoleFormValues = {
  role: string;
};

export function membershipRoleFields(
  defaultRole: string,
): readonly CreateField<MembershipRoleFormValues>[] {
  return [
    {
      name: "role",
      label: "Role",
      type: "select",
      defaultValue: defaultRole,
      options: Object.values(UpdateOrganizationMembershipRequestRole).map(
        (role) => ({
          value: role,
          label: role[0].toUpperCase() + role.slice(1),
        }),
      ),
    },
  ];
}

function isMembershipRole(
  value: string,
): value is UpdateOrganizationMembershipRequestRole {
  return Object.values(UpdateOrganizationMembershipRequestRole).includes(
    value as UpdateOrganizationMembershipRequestRole,
  );
}

export function membershipRolePayload(
  values: MembershipRoleFormValues,
): components["schemas"]["UpdateOrganizationMembershipRequest"] {
  if (!isMembershipRole(values.role)) {
    throw new Error("Role is not supported by the current Stealth API.");
  }
  return { role: values.role };
}
