import type { components } from "@/api/generated/schema";
import type { CreateField } from "@/components/create-dialog";
import { SITE_FRAMEWORK_OPTIONS } from "@/lib/capabilities/runtime-catalog";

export type SiteFormValues = {
  name: string;
};

export const siteFields: readonly CreateField<SiteFormValues>[] = [
  { name: "name", label: "Name", placeholder: "marketing-site" },
];

export function sitePayload(
  values: SiteFormValues,
): components["schemas"]["CreateSiteRequest"] {
  const name = values.name.trim();
  if (!name) throw new Error("Site name is required.");
  return {
    name,
    framework: SITE_FRAMEWORK_OPTIONS[0].value,
    enabled: true,
  };
}
