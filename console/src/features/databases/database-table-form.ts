import type { components } from "@/api/generated/schema";
import type { CreateField } from "@/components/create-dialog";
import { databaseName } from "@/features/databases/data-values";

export type DatabaseTableFormValues = {
  name: string;
  row_security: string;
};

export const databaseTableFields: readonly CreateField<DatabaseTableFormValues>[] =
  [
    {
      name: "name",
      label: "Table name",
      placeholder: "users",
      help: "Use 2–120 characters.",
    },
    {
      name: "row_security",
      label: "Row security",
      type: "select",
      defaultValue: "true",
      options: [
        { value: "true", label: "Enabled" },
        { value: "false", label: "Disabled" },
      ],
      help: "When enabled, individual row grants can allow access in addition to table grants.",
    },
  ];

export function databaseTablePayload(
  values: DatabaseTableFormValues,
): components["schemas"]["CreateDatabaseTableRequest"] {
  return {
    name: databaseName.parse(values.name),
    row_security: values.row_security !== "false",
  };
}
