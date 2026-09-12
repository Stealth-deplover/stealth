import { DatabaseColumnType } from "@/api/generated/schema";
import type { components } from "@/api/generated/schema";
import type { DatabaseColumn } from "@/api/types";
import type { CreateField } from "@/components/create-dialog";
import { parseColumn, parseRowData } from "@/features/databases/data-values";

export type DatabaseRowFormValues = {
  data: string;
};

export function databaseRowFields(
  defaultValue = "{}",
): readonly CreateField<DatabaseRowFormValues>[] {
  return [
    {
      name: "data",
      label: "Row data (JSON)",
      type: "textarea",
      defaultValue,
    },
  ];
}

export function databaseRowPayload(
  values: DatabaseRowFormValues,
  columns: DatabaseColumn[],
): components["schemas"]["CreateDatabaseRowRequest"] {
  return { data: parseRowData(values.data, columns) };
}

export function databasePartialRowPayload(
  values: DatabaseRowFormValues,
  columns: DatabaseColumn[],
): components["schemas"]["UpdateDatabaseRowRequest"] {
  return { data: parseRowData(values.data, columns, true) };
}

export type DatabaseColumnFormValues = {
  key: string;
  type: string;
  required: string;
  varchar_size: string;
  default: string;
};

export const databaseColumnFields: readonly CreateField<DatabaseColumnFormValues>[] =
  [
    {
      name: "key",
      label: "Column key",
      placeholder: "email",
      help: "Letters, numbers, and underscores; start with a letter or underscore. Maximum 120 characters.",
    },
    {
      name: "type",
      label: "Type",
      type: "select",
      defaultValue: "text",
      options: Object.values(DatabaseColumnType).map((value) => ({
        value,
        label: value,
      })),
    },
    {
      name: "required",
      label: "Required",
      type: "select",
      defaultValue: "false",
      options: [
        { value: "false", label: "No (nullable)" },
        { value: "true", label: "Yes (not nullable)" },
      ],
    },
    {
      name: "varchar_size",
      label: "Varchar size",
      required: false,
      help: "Required only for varchar, from 1 to 10000.",
    },
    {
      name: "default",
      label: "Default value (JSON)",
      type: "textarea",
      required: false,
      help: 'Leave blank for no default. Use JSON literals, for example "hello", true, or 42.',
    },
  ];

export function databaseColumnPayload(
  values: DatabaseColumnFormValues,
): components["schemas"]["CreateDatabaseColumnRequest"] {
  return parseColumn(values);
}
