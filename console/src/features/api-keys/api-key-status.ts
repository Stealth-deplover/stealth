import type { APIKey } from "@/api/types";

export type APIKeyStatus = "active" | "expired" | "revoked";

type APIKeyStatusInput = Pick<APIKey, "expires_at" | "revoked_at">;

export function getApiKeyStatus(
  key: APIKeyStatusInput,
  now: Date = new Date(),
): APIKeyStatus {
  if (key.revoked_at !== null) return "revoked";

  if (key.expires_at !== null && new Date(key.expires_at) <= now) {
    return "expired";
  }

  return "active";
}
