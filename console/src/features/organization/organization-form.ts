import type { components } from "@/api/generated/schema";
import type { CreateField } from "@/components/create-dialog";

export type OrganizationFormValues = {
  name: string;
  slug: string;
};

export const organizationFields: readonly CreateField<OrganizationFormValues>[] =
  [
    { name: "name", label: "Display name", placeholder: "Acme Inc" },
    {
      name: "slug",
      label: "Slug",
      placeholder: "acme-inc",
      help: "Lowercase letters, numbers, and hyphens.",
    },
  ];

export function organizationPayload(
  values: OrganizationFormValues,
): components["schemas"]["CreateOrganizationRequest"] {
  const name = values.name.trim();
  const slug = values.slug.trim();
  if (!name || !slug) throw new Error("Name and slug are required.");
  return { name, slug };
}
