import { describe, expect, it } from "vitest";
import { getSafeNextPath } from "@/lib/navigation";

describe("safe next paths", () => {
  it("keeps internal paths and rejects external destinations", () => {
    expect(getSafeNextPath("/accept-invitation?token=abc")).toBe(
      "/accept-invitation?token=abc",
    );
    expect(getSafeNextPath("//evil.example")).toBe("/organizations");
    expect(getSafeNextPath("https://evil.example")).toBe("/organizations");
    expect(getSafeNextPath(undefined)).toBe("/organizations");
  });
});
