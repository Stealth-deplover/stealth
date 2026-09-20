import { render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AdminAlertsView } from "./admin-alerts-view";

vi.mock("next/navigation", () => ({
  usePathname: () => "/admin/alerts",
}));

vi.mock("@/api/mutations", () => ({
  useCreateAdminAlert: () => ({
    error: null,
    isPending: false,
    mutate: vi.fn(),
  }),
  useDeleteAdminAlert: () => ({
    error: null,
    isPending: false,
    mutate: vi.fn(),
  }),
}));

vi.mock("@/api/queries", () => ({
  useAdminAlertEvents: () => ({
    data: {
      items: [
        {
          id: "event-deleted",
          rule_id: "rule-deleted",
          rule_name: "Deleted CPU threshold",
          rule_kind: "metric_threshold",
          severity: "critical",
          state: "firing",
          value: 91,
          message: "CPU threshold exceeded",
          occurred_at: "2026-09-20T00:00:00Z",
          source_rule_exists: false,
        },
        {
          id: "event-active",
          rule_id: "rule-active",
          rule_name: "Active API threshold",
          rule_kind: "service_health",
          severity: "warning",
          state: "resolved",
          value: null,
          message: "API recovered",
          occurred_at: "2026-09-20T00:01:00Z",
          source_rule_exists: true,
        },
      ],
    },
    error: null,
    isPending: false,
    refetch: vi.fn(),
  }),
  useAdminAlerts: () => ({
    data: {
      items: [
        {
          id: "rule-active",
          name: "Active API threshold",
          kind: "service_health",
          condition: {},
          severity: "warning",
          for_seconds: 0,
          enabled: true,
          state: "resolved",
          pending_since: null,
          last_evaluated_at: "2026-09-20T00:01:00Z",
          last_value: null,
          last_error: null,
          created_by_account_id: null,
          created_at: "2026-09-20T00:00:00Z",
          updated_at: "2026-09-20T00:01:00Z",
        },
      ],
    },
    error: null,
    isPending: false,
    refetch: vi.fn(),
  }),
  useAdminMonitors: () => ({ data: { items: [] } }),
}));

vi.mock("./admin-time-range", () => ({
  AdminTimeRange: () => null,
  useAdminTimeRange: () => ({
    rangeKey: "1h",
    refreshKey: "30s",
    customRange: undefined,
    refreshInterval: false,
    setRange: vi.fn(),
    setRefresh: vi.fn(),
    setCustomRange: vi.fn(),
  }),
}));

describe("AdminAlertsView", () => {
  it("renders retained deleted-rule history separately from active rules", () => {
    render(<AdminAlertsView />);

    expect(
      screen.getByRole("heading", { name: "Recent alert history" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Deleted CPU threshold")).toBeInTheDocument();
    expect(screen.getByText("Deleted rule")).toBeInTheDocument();
    expect(screen.getByText("Active rule")).toBeInTheDocument();

    const tables = screen.getAllByRole("table");
    expect(tables).toHaveLength(2);
    expect(within(tables[0]).queryByText("Deleted CPU threshold")).toBeNull();
    expect(
      within(tables[1]).getByText("Deleted CPU threshold"),
    ).toBeInTheDocument();
  });
});
