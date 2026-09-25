import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { LogsView } from "./logs-view";

vi.mock("@/api/queries", () => ({
  useFunctions: () => ({
    data: { functions: [] },
    error: null,
    isLoading: false,
    isFetching: false,
    refetch: vi.fn(),
  }),
  useSites: () => ({
    data: { sites: [] },
    error: null,
    isLoading: false,
    isFetching: false,
    refetch: vi.fn(),
  }),
  useApps: () => ({
    data: { apps: [{ id: "app-1", name: "api" }] },
    error: null,
    isLoading: false,
    isFetching: false,
    refetch: vi.fn(),
  }),
}));

vi.mock("@/hooks/use-cursor-pagination", () => ({
  useCursorPagination: () => ({
    cursor: undefined,
    goFirst: vi.fn(),
    goNext: vi.fn(),
    goPrevious: vi.fn(),
    canFirst: false,
    canPrevious: false,
  }),
}));

describe("LogsView", () => {
  it("includes Apps and describes runtime logs as a separate source", () => {
    render(<LogsView organizationId="org-1" projectId="project-1" />);

    expect(
      screen.getByText(
        "Functions provide build and execution logs, Sites provide build logs, and Apps provide build and runtime logs.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "api" })).toBeInTheDocument();
    expect(
      screen.getByText(
        "App build output and retained stdout/stderr from verified runtime containers.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "Open App logs →" }),
    ).toHaveAttribute(
      "href",
      "/organizations/org-1/projects/project-1/apps/app-1",
    );
    expect(screen.getByText("App log sources")).toBeInTheDocument();
  });
});
