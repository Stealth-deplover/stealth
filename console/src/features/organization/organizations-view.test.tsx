import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { OrganizationsIndexView } from "./organizations-view";

vi.mock("next/navigation", () => ({
  usePathname: () => "/organizations",
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  useSearchParams: () => new URLSearchParams(),
}));

vi.mock("@/api/mutations", () => ({
  useCreateOrganization: () => ({ isPending: false, mutateAsync: vi.fn() }),
}));

vi.mock("@/api/queries", () => ({
  useOrganizations: () => ({
    data: undefined,
    isError: false,
    isLoading: true,
    refetch: vi.fn(),
  }),
}));

describe("OrganizationsIndexView", () => {
  it("announces the organization loading state once", () => {
    render(<OrganizationsIndexView />);

    expect(
      screen.getByRole("status", { name: "Loading organizations" }),
    ).toHaveAttribute("aria-busy", "true");
    expect(screen.getByText("Loading organizations…")).toHaveClass("sr-only");
  });
});
