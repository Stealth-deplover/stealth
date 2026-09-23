import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  AppRuntime_status,
  WorkloadHealthCheckProtocol,
} from "@/api/generated/schema";
import type { StealthApp } from "@/api/types";
import { AppDetailView } from "@/features/apps/app-detail-view";

const mocks = vi.hoisted(() => ({ app: null as StealthApp | null }));

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn() }),
}));

vi.mock("@/api/queries", () => ({
  useApp: () => ({
    data: mocks.app ? { app: mocks.app } : undefined,
    error: null,
    isError: false,
    isPending: false,
    refetch: vi.fn(),
  }),
  useApps: () => ({ data: { can_manage: false } }),
}));

vi.mock("@/api/mutations", () => ({
  useDeleteApp: () => ({ isPending: false, mutateAsync: vi.fn() }),
  useUpdateApp: () => ({ isPending: false, mutateAsync: vi.fn() }),
}));

function makeApp(): StealthApp {
  return {
    id: "app-1",
    project_id: "project-1",
    name: "backend",
    enabled: true,
    platform_hostname: null,
    workload: {
      schema_version: "v1",
      port: 8080,
      command: [],
      working_directory: null,
      health_check: {
        protocol: WorkloadHealthCheckProtocol.tcp,
        path: null,
        interval_seconds: 10,
        timeout_seconds: 2,
        initial_delay_seconds: 5,
        failure_threshold: 3,
      },
      resources: {
        cpu_millis: 500,
        memory_bytes: 536870912,
        pids_limit: 256,
      },
      stop_grace_period_seconds: 15,
      restart_policy: "always",
    },
    workload_spec_sha256: "a".repeat(64),
    desired_generation: 1,
    observed_generation: 0,
    runtime_status: AppRuntime_status.not_deployed,
    runtime_error: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

describe("AppDetailView", () => {
  beforeEach(() => {
    mocks.app = makeApp();
  });

  it("states that the App has no deployment and offers no fake deploy action", () => {
    render(
      <AppDetailView
        organizationId="org-1"
        projectId="project-1"
        appId="app-1"
      />,
    );

    expect(
      screen.getByText("No deployment has been created for this App yet."),
    ).toBeInTheDocument();
    expect(screen.getAllByText("Not deployed")).toHaveLength(2);
    expect(screen.queryByRole("button", { name: /deploy/i })).toBeNull();
  });
});
