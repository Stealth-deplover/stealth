import { describe, expect, it } from "vitest";
import { CreateProjectAPIKeyRequestScopes } from "@/api/generated/schema";
import {
  apiKeyPayload,
  type APIKeyFormValues,
} from "@/features/api-keys/api-key-form";

const values: APIKeyFormValues = {
  name: "  deploy key  ",
  scopes: ` users.read, ${CreateProjectAPIKeyRequestScopes.storage_write} `,
  expires_at: "2030-01-02T03:04",
};

describe("API key form adapter", () => {
  it("maps typed form values to the API payload", () => {
    expect(apiKeyPayload(values)).toEqual({
      name: "deploy key",
      scopes: [
        CreateProjectAPIKeyRequestScopes.users_read,
        CreateProjectAPIKeyRequestScopes.storage_write,
      ],
      expires_at: "2030-01-02T03:04:00.000Z",
    });
  });

  it("rejects empty or unknown permissions", () => {
    expect(() => apiKeyPayload({ ...values, scopes: "" })).toThrow(
      "Select at least one permission.",
    );
    expect(() => apiKeyPayload({ ...values, scopes: "admin.root" })).toThrow(
      "Permission is not supported: admin.root",
    );
  });

  it("rejects malformed expiry dates", () => {
    expect(() =>
      apiKeyPayload({ ...values, expires_at: "not-a-date" }),
    ).toThrow("Enter a valid expiry date.");
  });
});
