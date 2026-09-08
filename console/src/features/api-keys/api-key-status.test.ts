import { describe, expect, it } from "vitest";
import { getApiKeyStatus } from "@/features/api-keys/api-key-status";

const now = new Date("2026-01-01T00:00:00.000Z");

describe("getApiKeyStatus", () => {
  it("returns active when the key is not revoked and has a future expiry", () => {
    expect(
      getApiKeyStatus(
        {
          revoked_at: null,
          expires_at: "2026-01-02T00:00:00.000Z",
        },
        now,
      ),
    ).toBe("active");
  });

  it("returns expired when the key is not revoked and has a past expiry", () => {
    expect(
      getApiKeyStatus(
        {
          revoked_at: null,
          expires_at: "2025-12-31T23:59:59.000Z",
        },
        now,
      ),
    ).toBe("expired");
  });

  it("returns revoked when the key has been revoked", () => {
    expect(
      getApiKeyStatus(
        {
          revoked_at: "2026-01-01T00:00:00.000Z",
          expires_at: "2026-01-02T00:00:00.000Z",
        },
        now,
      ),
    ).toBe("revoked");
  });

  it("prioritizes revoked over expired", () => {
    expect(
      getApiKeyStatus(
        {
          revoked_at: "2026-01-01T00:00:00.000Z",
          expires_at: "2025-12-31T23:59:59.000Z",
        },
        now,
      ),
    ).toBe("revoked");
  });
});
