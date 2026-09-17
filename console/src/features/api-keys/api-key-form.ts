import { CreateProjectAPIKeyRequestScopes } from "@/api/generated/schema";
import type { components } from "@/api/generated/schema";
import type { CreateField } from "@/components/create-dialog";
import {
  formatAPIKeyScope,
  parseCommaSeparatedValues,
} from "@/features/integrations/integration-values";

export type APIKeyFormValues = {
  name: string;
  scopes: string;
  expires_at: string;
};

const scopeValues = Object.values(CreateProjectAPIKeyRequestScopes);
const scopeSet = new Set<string>(scopeValues);

export function apiKeyFields(): CreateField<APIKeyFormValues>[] {
  return [
    { name: "name", label: "Name", placeholder: "CI deploy key" },
    {
      name: "scopes",
      label: "Permissions",
      type: "multiselect",
      options: scopeValues.map((scope) => ({
        value: scope,
        label: formatAPIKeyScope(scope),
      })),
      help: "Select the exact API scopes this integration needs. Read and write access are separate permissions.",
    },
    {
      name: "expires_at",
      label: "Expires at",
      type: "datetime-local",
      required: false,
      help: "Optional. The Go API accepts future expiries within its configured limit.",
    },
  ];
}

export function apiKeyPayload(
  values: APIKeyFormValues,
): components["schemas"]["CreateProjectAPIKeyRequest"] {
  const name = values.name.trim();
  if (!name) throw new Error("Name is required.");

  const selected = parseCommaSeparatedValues(values.scopes);
  const invalid = selected.find((scope) => !scopeSet.has(scope));
  if (invalid) throw new Error(`Permission is not supported: ${invalid}`);
  if (!selected.length) throw new Error("Select at least one permission.");

  const expiresAt = values.expires_at ? new Date(values.expires_at) : undefined;
  if (expiresAt && Number.isNaN(expiresAt.valueOf())) {
    throw new Error("Enter a valid expiry date.");
  }

  return {
    name,
    scopes:
      selected as components["schemas"]["CreateProjectAPIKeyRequest"]["scopes"],
    expires_at: expiresAt?.toISOString() ?? null,
  };
}
