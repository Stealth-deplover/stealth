import type { components } from "@/api/generated/schema";
import type { CreateField } from "@/components/create-dialog";
import { databaseName } from "@/features/databases/data-values";

export type DatabaseFormValues = {
  name: string;
};

export const databaseFields: readonly CreateField<DatabaseFormValues>[] = [
  { name: "name", label: "Name", placeholder: "primary" },
];

export function databasePayload(
  values: DatabaseFormValues,
): components["schemas"]["CreateDatabaseRequest"] {
  return { name: databaseName.parse(values.name) };
}
