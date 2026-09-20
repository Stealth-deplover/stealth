import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AdminInfrastructureView } from "./admin-infrastructure-view";

vi.mock("next/navigation", () => ({
  usePathname: () => "/admin/infrastructure",
}));

vi.mock("@/api/queries", () => ({
  useAdminInfrastructure: () => ({
    data: { items: [] },
    error: null,
    isPending: false,
    refetch: vi.fn(),
  }),
}));

describe("infrastructure scope filter", () => {
  it("uses pressed filter buttons instead of incomplete tab semantics", () => {
    render(<AdminInfrastructureView />);

    expect(
      screen.getByRole("group", { name: "Infrastructure scope" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("tablist")).not.toBeInTheDocument();

    const host = screen.getByRole("button", { name: "Host" });
    const containers = screen.getByRole("button", { name: "Containers" });
    expect(host).toHaveAttribute("aria-pressed", "true");
    expect(host).toHaveClass("min-h-11");

    fireEvent.click(containers);

    expect(host).toHaveAttribute("aria-pressed", "false");
    expect(containers).toHaveAttribute("aria-pressed", "true");
  });
});
