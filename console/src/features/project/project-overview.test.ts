import { describe, expect, it } from "vitest";
import {
  isEmptyProject,
  shouldShowQuickStart,
} from "@/features/project/project-overview";

describe("isEmptyProject", () => {
  it("requires loaded usage and storage data before showing onboarding", () => {
    const usage = { function_count: 0, site_count: 0, database_count: 0 };

    expect(isEmptyProject(undefined, 0)).toBe(false);
    expect(isEmptyProject(usage, undefined)).toBe(false);
    expect(isEmptyProject(usage, 0)).toBe(true);
  });

  it("hides onboarding once a project has a measured resource", () => {
    expect(
      isEmptyProject(
        { function_count: 1, site_count: 0, database_count: 0 },
        0,
      ),
    ).toBe(false);
    expect(
      isEmptyProject(
        { function_count: 0, site_count: 0, database_count: 0 },
        1,
      ),
    ).toBe(false);
  });
});

describe("shouldShowQuickStart", () => {
  const usage = { function_count: 0, site_count: 0, database_count: 0 };

  it("requires the backend capability before showing create actions", () => {
    expect(shouldShowQuickStart(false, usage, 0)).toBe(false);
    expect(shouldShowQuickStart(undefined, usage, 0)).toBe(false);
    expect(shouldShowQuickStart(true, usage, 0)).toBe(true);
  });

  it("does not show onboarding when usage or storage summary is unavailable", () => {
    expect(shouldShowQuickStart(true, undefined, 0)).toBe(false);
    expect(shouldShowQuickStart(true, usage, undefined)).toBe(false);
  });
});
