import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AdminIncidentsView } from "./admin-incidents-view";

vi.mock("next/navigation", () => ({
  usePathname: () => "/admin/incidents",
}));

vi.mock("@/api/mutations", () => ({
  useAddAdminIncidentEvent: () => ({
    error: null,
    isPending: false,
    mutate: vi.fn(),
  }),
  useCreateAdminIncident: () => ({
    error: null,
    isPending: false,
    mutate: vi.fn(),
  }),
  useUpdateAdminIncident: () => ({
    error: null,
    isPending: false,
    mutate: vi.fn(),
  }),
}));

vi.mock("@/api/queries", () => ({
  useAdminIncident: () => ({
    data: undefined,
    error: null,
    isPending: false,
    refetch: vi.fn(),
  }),
  useAdminIncidents: () => ({
    data: {
      items: [
        {
          id: "incident-1",
          title: "API unavailable",
          severity: "critical",
          services: ["api"],
          started_at: "2026-09-20T00:00:00Z",
          status: "investigating",
        },
      ],
    },
    error: null,
    isPending: false,
    refetch: vi.fn(),
  }),
}));

describe("AdminIncidentsView", () => {
  it("uses a named button to open incident details instead of a focusable row", () => {
    render(<AdminIncidentsView />);

    const action = screen.getByRole("button", {
      name: "Open incident: API unavailable",
    });
    expect(action.closest("tr")).not.toHaveAttribute("tabindex");

    fireEvent.click(action);
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });
});
