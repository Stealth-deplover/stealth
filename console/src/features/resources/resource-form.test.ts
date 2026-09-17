import { describe, expect, it } from "vitest";
import type { components } from "@/api/generated/schema";
import { databasePayload } from "@/features/databases/database-form";
import { functionPayload } from "@/features/functions/function-form";
import { organizationPayload } from "@/features/organization/organization-form";
import { projectPayload } from "@/features/organization/project-form";
import { sitePayload } from "@/features/sites/site-form";
import { storagePayload } from "@/features/storage/storage-form";
import { userPayload } from "@/features/users/user-form";

describe("resource form adapters", () => {
  it("maps organization and project values after trimming", () => {
    expect(organizationPayload({ name: " Acme ", slug: " acme " })).toEqual({
      name: "Acme",
      slug: "acme",
    });
    expect(projectPayload({ name: " Production API " })).toEqual({
      name: "Production API",
    });
  });

  it("keeps server-owned resource defaults in the payload", () => {
    expect(sitePayload({ name: " marketing " }).enabled).toBe(true);
    expect(storagePayload({ name: " assets " })).toEqual({
      name: "assets",
      file_security: true,
    });
    expect(
      functionPayload({
        name: " api ",
        runtime: "node-22",
        entrypoint: " src/index.main ",
        description: " description ",
      }),
    ).toMatchObject({
      name: "api",
      entrypoint: "src/index.main",
      commands: "",
      timeout_seconds: 15,
      enabled: true,
      logging: true,
      description: "description",
    });
  });

  it("uses resource naming validators before sending payloads", () => {
    expect(databasePayload({ name: " primary " })).toEqual({
      name: "primary",
    });
    expect(() => storagePayload({ name: "../private" })).toThrow();
    expect(() => databasePayload({ name: "x" })).toThrow();
  });

  it("keeps user passwords exact while normalizing optional identity fields", () => {
    expect(
      userPayload({
        email: " owner@example.test ",
        password: "  exact password  ",
        name: " Owner ",
      }),
    ).toEqual({
      email: "owner@example.test",
      password: "  exact password  ",
      name: "Owner",
    } satisfies components["schemas"]["CreateProjectUserRequest"]);
  });
});
