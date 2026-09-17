import type { components } from "@/api/generated/schema";
import type { CreateField } from "@/components/create-dialog";

export type ProjectFormValues = {
  name: string;
};

export const projectFields: readonly CreateField<ProjectFormValues>[] = [
  {
    name: "name",
    label: "Project name",
    placeholder: "Production API",
    help: "Use a readable name; the console sends the lowercase hyphenated slug required by the API.",
  },
];

export function projectPayload(
  values: ProjectFormValues,
): components["schemas"]["CreateProjectRequest"] {
  const name = values.name.trim();
  if (!name) throw new Error("Project name is required.");
  return { name };
}
