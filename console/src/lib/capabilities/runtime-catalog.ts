import { CreateSiteRequestFramework, FunctionRuntime } from "@/api/generated/schema";

/**
 * Temporary console fallback: the current Go API exposes the runtime enum in
 * OpenAPI, but does not expose a runtime/capability catalog endpoint.
 */
export const FUNCTION_RUNTIME_OPTIONS = [
  { value: FunctionRuntime.node_22, label: "Node.js 22" },
  { value: FunctionRuntime.python_3_13, label: "Python 3.13" },
  { value: FunctionRuntime.go_1_24, label: "Go 1.24" },
] as const;

export const FUNCTION_RUNTIME_VALUES = FUNCTION_RUNTIME_OPTIONS.map((option) => option.value);

export function isFunctionRuntime(value: string): value is FunctionRuntime {
  return FUNCTION_RUNTIME_VALUES.includes(value as FunctionRuntime);
}

/** The current contract only exposes one site framework; keep it generated and centralized. */
export const SITE_FRAMEWORK_OPTIONS = [
  { value: CreateSiteRequestFramework.static, label: "Static" },
] as const;
