import type { components } from "@/api/generated/schema";
import type { CreateField } from "@/components/create-dialog";
import {
  FUNCTION_RUNTIME_OPTIONS,
  isFunctionRuntime,
} from "@/lib/capabilities/runtime-catalog";

export type FunctionFormValues = {
  name: string;
  runtime: string;
  entrypoint: string;
  description: string;
};

export const functionFields: readonly CreateField<FunctionFormValues>[] = [
  { name: "name", label: "Name", placeholder: "api-handler" },
  {
    name: "runtime",
    label: "Runtime",
    type: "select",
    defaultValue: FUNCTION_RUNTIME_OPTIONS[0].value,
    options: FUNCTION_RUNTIME_OPTIONS,
    help: "Available runtimes are centralized from the current OpenAPI enum.",
  },
  {
    name: "entrypoint",
    label: "Entrypoint",
    placeholder: "src/index.main",
  },
  {
    name: "description",
    label: "Description",
    type: "textarea",
    required: false,
  },
];

export function functionPayload(
  values: FunctionFormValues,
): components["schemas"]["CreateFunctionRequest"] {
  const name = values.name.trim();
  const runtime = values.runtime.trim();
  const entrypoint = values.entrypoint.trim();
  if (!name || !entrypoint) {
    throw new Error("Name and entrypoint are required.");
  }
  if (!isFunctionRuntime(runtime)) {
    throw new Error("Runtime is not supported by the current Stealth API.");
  }
  return {
    name,
    runtime,
    entrypoint,
    commands: "",
    timeout_seconds: 15,
    enabled: true,
    logging: true,
    description: values.description.trim(),
  };
}
