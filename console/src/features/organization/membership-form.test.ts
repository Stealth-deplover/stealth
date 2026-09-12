import { describe, expect, it } from "vitest";
import { membershipRolePayload } from "@/features/organization/membership-form";

describe("membership form adapter", () => {
  it("maps supported roles to the generated request shape", () => {
    expect(membershipRolePayload({ role: "viewer" })).toEqual({
      role: "viewer",
    });
  });

  it("rejects roles outside the API enum", () => {
    expect(() => membershipRolePayload({ role: "owner" })).toThrow();
  });
});
