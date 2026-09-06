import { describe, expect, it } from "vitest";
import { cn, getInitials, humanize, isRecord } from "@/lib/utils";

describe("console utility helpers", () => {
  it("merges utility classes with later precedence", () => {
    expect(cn("px-2 text-white", "px-4")).toBe("text-white px-4");
  });

  it("creates compact initials and humanized labels", () => {
    expect(getInitials("Acme Platform")).toBe("AP");
    expect(humanize("build_status")).toBe("Build Status");
  });

  it("narrows records without accepting null", () => {
    expect(isRecord({ status: 200 })).toBe(true);
    expect(isRecord(null)).toBe(false);
  });
});
