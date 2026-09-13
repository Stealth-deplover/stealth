import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Organization } from "@/api/types";
import { OrganizationOverviewView } from "./overview-view";

const mocks = vi.hoisted(() => ({
  organization: {
    data: {
      id: "org-1",
      name: "Acme",
      slug: "acme",
      created_at: "2026-01-01T00:00:00Z",
    } as Organization | undefined,
    error: null as unknown,
    isError: false,
    isPending: false,
    refetch: vi.fn(),
  },
  plan: {
    data: {
      plan: {
        organization_id: "org-1",
        plan_key: "free",
        status: "active",
        current_period_start: "2026-01-01",
        current_period_end: "2026-02-01",
        limits: {
          projects: 3,
          members: 5,
          databases: 2,
          storage_buckets: 2,
          functions: 5,
          sites: 2,
        },
        usage: {
          projects: 1,
          members: 2,
          databases: 1,
          storage_buckets: 0,
          functions: 1,
          sites: 0,
        },
      },
    },
    error: null as unknown,
    isError: false,
    isPending: false,
    refetch: vi.fn(),
  },
}));

vi.mock("next/link", () => ({
  default: ({
    children,
    href,
  }: {
    children: React.ReactNode;
    href: string;
  }) => <a href={href}>{children}</a>,
}));

vi.mock("@/api/queries", () => ({
  useOrganization: () => mocks.organization,
  useOrganizationPlan: () => mocks.plan,
}));

vi.mock("./projects-view", () => ({
  OrganizationProjectList: () => <div>Projects list</div>,
}));

describe("organization overview states", () => {
  beforeEach(() => {
    mocks.organization.data = {
      id: "org-1",
      name: "Acme",
      slug: "acme",
      created_at: "2026-01-01T00:00:00Z",
    };
    mocks.organization.error = null;
    mocks.organization.isError = false;
    mocks.organization.isPending = false;
    mocks.organization.refetch.mockReset();
    mocks.plan.data = {
      plan: {
        organization_id: "org-1",
        plan_key: "free",
        status: "active",
        current_period_start: "2026-01-01",
        current_period_end: "2026-02-01",
        limits: {
          projects: 3,
          members: 5,
          databases: 2,
          storage_buckets: 2,
          functions: 5,
          sites: 2,
        },
        usage: {
          projects: 1,
          members: 2,
          databases: 1,
          storage_buckets: 0,
          functions: 1,
          sites: 0,
        },
      },
    };
    mocks.plan.error = null;
    mocks.plan.isError = false;
    mocks.plan.isPending = false;
    mocks.plan.refetch.mockReset();
  });

  it("shows loading state while either overview query is pending", () => {
    mocks.plan.isPending = true;

    render(<OrganizationOverviewView organizationId="org-1" />);

    expect(screen.getByLabelText("Loading")).toBeVisible();
    expect(screen.getByText("Projects list")).toBeVisible();
  });

  it("shows a retryable error when the organization query fails", () => {
    mocks.organization.data = undefined;
    mocks.organization.error = new Error("request failed");
    mocks.organization.isError = true;

    render(<OrganizationOverviewView organizationId="org-1" />);

    expect(screen.getByRole("alert")).toHaveTextContent(
      "Could not load organization",
    );
    expect(screen.getByRole("button", { name: "Retry" })).toBeVisible();
  });

  it("shows the organization overview only after both responses are present", () => {
    render(<OrganizationOverviewView organizationId="org-1" />);

    expect(screen.getByRole("heading", { name: "Acme" })).toBeVisible();
    expect(screen.getByText("free")).toBeVisible();
    expect(screen.getByText("Projects list")).toBeVisible();
  });
});
