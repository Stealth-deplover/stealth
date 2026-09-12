import { CreateFunctionVariableRequestKind } from "@/api/generated/schema";
import type { components } from "@/api/generated/schema";
import type { CreateField } from "@/components/create-dialog";

export type FunctionVariableFormValues = {
  key: string;
  value: string;
  kind: string;
  description: string;
};

export const functionVariableFields: readonly CreateField<FunctionVariableFormValues>[] =
  [
    { name: "key", label: "Key", placeholder: "DATABASE_URL" },
    { name: "value", label: "Value", type: "password" },
    {
      name: "kind",
      label: "Kind",
      type: "select",
      defaultValue: "variable",
      options: [
        { value: "variable", label: "Variable" },
        { value: "secret", label: "Secret" },
      ],
      help: "Secret values are marked as sensitive.",
    },
    { name: "description", label: "Description", required: false },
  ];

function isFunctionVariableKind(
  value: string,
): value is CreateFunctionVariableRequestKind {
  return Object.values(CreateFunctionVariableRequestKind).includes(
    value as CreateFunctionVariableRequestKind,
  );
}

export function functionVariablePayload(
  values: FunctionVariableFormValues,
): components["schemas"]["CreateFunctionVariableRequest"] {
  const key = values.key.trim();
  if (!key) throw new Error("Key is required.");
  if (!values.value) throw new Error("Value is required.");
  if (!isFunctionVariableKind(values.kind)) {
    throw new Error("Kind must be variable or secret.");
  }
  const description = values.description.trim();
  return {
    key,
    value: values.value,
    kind: values.kind,
    is_secret: values.kind === CreateFunctionVariableRequestKind.secret,
    ...(description ? { description } : {}),
  };
}
