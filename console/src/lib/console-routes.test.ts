import { describe, expect, it } from "vitest";
import {
  organizationPath,
  organizationProjectsPath,
  organizationsPath,
  parseConsoleRoute,
  projectPath,
} from "@/lib/console-routes";

describe("console routes", () => {
  it("parses organization and project context from canonical console paths", () => {
    expect(
      parseConsoleRoute(
        "/organizations/org-123/projects/project-456/functions/function-789",
      ),
    ).toEqual({
      pathname:
        "/organizations/org-123/projects/project-456/functions/function-789",
      organizationId: "org-123",
      projectId: "project-456",
      organizationBase: "/organizations/org-123",
      projectBase: "/organizations/org-123/projects/project-456",
      organizationSegments: [
        "projects",
        "project-456",
        "functions",
        "function-789",
      ],
      projectSegments: ["functions", "function-789"],
    });
  });

  it("keeps organization context without inventing a missing project", () => {
    expect(parseConsoleRoute("/organizations/org-123/projects")).toEqual({
      pathname: "/organizations/org-123/projects",
      organizationId: "org-123",
      organizationBase: "/organizations/org-123",
      organizationSegments: ["projects"],
      projectSegments: [],
    });
  });

  it("does not infer console context from unrelated paths", () => {
    expect(parseConsoleRoute("/account")).toEqual({
      pathname: "/account",
      organizationSegments: [],
      projectSegments: [],
    });
  });

  it("builds canonical paths and encodes dynamic route segments", () => {
    expect(organizationsPath()).toBe("/organizations");
    expect(organizationPath("org / 123", "members")).toBe(
      "/organizations/org%20%2F%20123/members",
    );
    expect(organizationProjectsPath("org-123")).toBe(
      "/organizations/org-123/projects",
    );
    expect(projectPath("org-123", "project-456", "observability", "logs")).toBe(
      "/organizations/org-123/projects/project-456/observability/logs",
    );
  });
});
