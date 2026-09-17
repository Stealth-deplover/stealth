import { z } from "zod";
import { DatabaseColumnType } from "@/api/generated/schema";
import type { DatabaseColumn } from "@/api/types";
import { formatDate } from "@/lib/format";

export const databaseName = z.string().trim().min(2).max(120);
export const columnRequest = z
  .object({
    key: z.string().regex(/^[A-Za-z_][A-Za-z0-9_]{0,119}$/),
    type: z.enum(DatabaseColumnType),
    required: z.boolean(),
    varchar_size: z.number().int().min(1).max(10000).optional(),
    default: z.unknown().optional(),
  })
  .refine(
    (value) =>
      value.type === "varchar"
        ? value.varchar_size !== undefined
        : value.varchar_size === undefined,
    {
      message:
        "Varchar requires a size from 1 to 10000; other types must leave size empty.",
    },
  );

export type DatabaseColumnFormValues = {
  key: string;
  type: string;
  required: string;
  varchar_size: string;
  default: string;
};

function validateValue(
  value: unknown,
  column: Pick<DatabaseColumn, "key" | "type" | "required" | "varchar_size">,
) {
  if (value === null && !column.required) return;
  const valid =
    column.type === "json"
      ? value !== undefined && (value !== null || !column.required)
      : column.type === "boolean"
        ? typeof value === "boolean"
        : column.type === "integer"
          ? typeof value === "number" && Number.isSafeInteger(value)
          : column.type === "double"
            ? typeof value === "number" && Number.isFinite(value)
            : column.type === "datetime"
              ? typeof value === "string" &&
                /^\d{4}-\d{2}-\d{2}T/.test(value) &&
                Number.isFinite(Date.parse(value))
              : typeof value === "string" &&
                (column.type !== "varchar" ||
                  Array.from(value).length <= (column.varchar_size ?? 10000));
  if (!valid)
    throw new Error(
      `Invalid value for ${column.key}: expected ${column.type}${column.required ? " (required)" : " or null"}.`,
    );
}

export function parseRowData(
  raw: string,
  columns: DatabaseColumn[],
  partial = false,
): Record<string, unknown> {
  let data: unknown;
  try {
    data = JSON.parse(raw);
  } catch {
    throw new Error("Enter valid JSON row data.");
  }
  if (!data || typeof data !== "object" || Array.isArray(data))
    throw new Error("Row data must be a JSON object.");
  const record = data as Record<string, unknown>;
  for (const [key, value] of Object.entries(record)) {
    const column = columns.find((item) => item.key === key);
    if (!column)
      throw new Error(
        `Unknown column: ${key}. Create the column in Schema first.`,
      );
    validateValue(value, column);
  }
  if (!partial)
    for (const column of columns) {
      if (
        column.required &&
        column.default == null &&
        !Object.hasOwn(record, column.key)
      )
        throw new Error(`${column.key} is required.`);
    }
  return record;
}

export function parseColumn(values: DatabaseColumnFormValues) {
  const result = columnRequest.parse({
    key: values.key.trim(),
    type: values.type,
    required: values.required === "true",
    ...(values.varchar_size
      ? { varchar_size: Number(values.varchar_size) }
      : {}),
    ...(values.default?.trim()
      ? { default: JSON.parse(values.default) as unknown }
      : {}),
  });
  if (Object.hasOwn(result, "default")) validateValue(result.default, result);
  return result;
}

export function displayRowValue(value: unknown, type?: string) {
  if (value === null) return "null";
  if (value === undefined) return "Not available";
  if (type === "datetime" && typeof value === "string")
    return formatDate(value);
  if (typeof value === "object") return JSON.stringify(value, null, 2);
  return String(value);
}
