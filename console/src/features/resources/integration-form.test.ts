import { describe, expect, it } from "vitest";
import {
  CreateFunctionVariableRequestKind,
  type components,
} from "@/api/generated/schema";
import { organizationInvitationPayload } from "@/features/organization/invitation-form";
import { functionVariablePayload } from "@/features/functions/function-variable-form";
import {
  webhookPayload,
  webhookUpdatePayload,
} from "@/features/webhooks/webhook-form";

describe("integration form adapters", () => {
  it("normalizes invitations and validates the role enum", () => {
    expect(
      organizationInvitationPayload({
        email: " member@example.test ",
        role: "developer",
      }),
    ).toEqual({ email: "member@example.test", role: "developer" });
    expect(() =>
      organizationInvitationPayload({
        email: "member@example.test",
        role: "owner",
      }),
    ).toThrow();
  });

  it("keeps function variable secrets exact while normalizing metadata", () => {
    expect(
      functionVariablePayload({
        key: " DATABASE_URL ",
        value: "  secret value  ",
        kind: CreateFunctionVariableRequestKind.secret,
        description: " Database connection ",
      }),
    ).toEqual({
      key: "DATABASE_URL",
      value: "  secret value  ",
      kind: CreateFunctionVariableRequestKind.secret,
      is_secret: true,
      description: "Database connection",
    } satisfies components["schemas"]["CreateFunctionVariableRequest"]);
  });

  it("maps webhook create and update values through one typed adapter", () => {
    const values = {
      name: " production ",
      url: " https://example.test/hooks ",
      events: "deploy.succeeded, audit.created",
      enabled: "false",
    };
    expect(webhookPayload(values)).toEqual({
      name: "production",
      url: "https://example.test/hooks",
      events: ["deploy.succeeded", "audit.created"],
      enabled: false,
    });
    expect(webhookUpdatePayload(values)).toEqual(webhookPayload(values));
    expect(() =>
      webhookPayload({ ...values, name: " ", url: "https://example.test" }),
    ).toThrow();
  });
});
