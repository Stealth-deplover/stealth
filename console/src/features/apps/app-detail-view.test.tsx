import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  AppHealth_status,
  AppRoute_status,
  AppDeploymentBuild_status,
  AppDeploymentPlatform,
  AppDeploymentSource,
  AppDeploymentStatus,
  AppRuntime_status,
  WorkloadHealthCheckProtocol,
} from "@/api/generated/schema";
import type { AppDeployment, StealthApp } from "@/api/types";
import { AppDetailView } from "@/features/apps/app-detail-view";

const mocks = vi.hoisted(() => ({
  app: null as StealthApp | null,
  deployments: [] as AppDeployment[],
  canManage: false,
}));

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
  useApps: () => ({ data: { can_manage: mocks.canManage } }),
  useAppDeployments: () => ({
    data: { deployments: mocks.deployments, pagination: { next_cursor: null } },
    error: null,
    isError: false,
    isPending: false,
    isFetching: false,
    refetch: vi.fn(),
  }),
}));

vi.mock("@/api/mutations", () => ({
  useCreateAppDeployment: () => ({ isPending: false, mutateAsync: vi.fn() }),
  useDeleteApp: () => ({ isPending: false, mutateAsync: vi.fn() }),
  useSelectAppDeployment: () => ({ isPending: false, mutateAsync: vi.fn() }),
  useUpdateApp: () => ({ isPending: false, mutateAsync: vi.fn() }),
}));

vi.mock("@/components/log-viewer", async () => {
  const React = await import("react");
  return {
    createLogSource: vi.fn(() => ({ key: "app-build", fetchPage: async () => [] })),
    LogViewer: ({ title, description }: { title: string; description: string }) =>
      React.createElement("div", { "data-testid": "app-build-logs" }, `${title}: ${description}`),
  };
});

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

function makeApp(): StealthApp {
  return {
    id: "app-1",
    project_id: "project-1",
    name: "backend",
    enabled: true,
    platform_hostname: null,
    desired_deployment_id: null,
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
    health_status: AppHealth_status.pending,
    route_status: AppRoute_status.not_available,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

function makeDeployment(overrides: Partial<AppDeployment> = {}): AppDeployment {
  return {
    id: "deployment-1",
    app_id: "app-1",
    project_id: "project-1",
    version: 1,
    source: AppDeploymentSource.upload,
    source_name: "source.tar",
    source_size_bytes: 64,
    source_checksum_sha256: "b".repeat(64),
    dockerfile_path: "Dockerfile",
    context_directory: ".",
    target: null,
    platform: AppDeploymentPlatform.linux_amd64,
    workload_snapshot: makeApp().workload,
    workload_spec_sha256: "a".repeat(64),
    status: AppDeploymentStatus.ready,
    build_status: AppDeploymentBuild_status.succeeded,
    error_message: null,
    image_digest: `sha256:${"c".repeat(64)}`,
    image_archive_sha256: "d".repeat(64),
    image_size_bytes: 512,
    selected: true,
    created_by_account_id: null,
    queued_at: "2026-01-01T00:00:00Z",
    build_started_at: "2026-01-01T00:00:01Z",
    built_at: "2026-01-01T00:00:02Z",
    finished_at: "2026-01-01T00:00:02Z",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:02Z",
    ...overrides,
  };
}

describe("AppDetailView", () => {
  beforeEach(() => {
    mocks.app = makeApp();
    mocks.deployments = [];
    mocks.canManage = false;
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
    expect(screen.getAllByText("Not deployed")).toHaveLength(1);
    expect(screen.queryByRole("button", { name: /deploy/i })).toBeNull();
  });

  it("shows a selected ready image without claiming a running runtime and renders build logs", () => {
    const app = makeApp();
    app.desired_deployment_id = "deployment-1";
    app.runtime_status = AppRuntime_status.pending;
    app.route_status = AppRoute_status.waiting_for_runtime;
    mocks.app = app;
    mocks.deployments = [makeDeployment()];
    render(
      <AppDetailView
        organizationId="org-1"
        projectId="project-1"
        appId="app-1"
      />,
    );

    expect(screen.getByText("Desired image selected")).toBeInTheDocument();
    expect(screen.getByText(/The runtime is reconciling generation 1/)).toBeInTheDocument();
    expect(screen.getByText("Pending")).toBeInTheDocument();
    expect(screen.queryByText("Running")).toBeNull();
    expect(screen.queryByText("Healthy")).toBeNull();
    expect(screen.getByText("Starting")).toBeInTheDocument();
    expect(screen.getByText("Waiting for runtime")).toBeInTheDocument();
    expect(screen.getByText(/public route waits for the current generation to pass its configured health check/i)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Inspect" }));
    expect(screen.getAllByText(`sha256:${"c".repeat(64)}`)).toHaveLength(2);
    expect(screen.getByTestId("app-build-logs")).toHaveTextContent("Build logs");
    expect(screen.getByTestId("app-build-logs")).toHaveTextContent("Runtime logs are not available");
  });

  it("shows a healthy current generation with its active hostname", () => {
    const app = makeApp();
    app.platform_hostname = "backend.apps.example.com";
    app.desired_deployment_id = "deployment-1";
    app.desired_generation = 3;
    app.observed_generation = 3;
    app.runtime_status = AppRuntime_status.running;
    app.health_status = AppHealth_status.healthy;
    app.route_status = AppRoute_status.active;
    mocks.app = app;
    mocks.deployments = [makeDeployment()];

    render(<AppDetailView organizationId="org-1" projectId="project-1" appId="app-1" />);

    expect(screen.getByText("Running")).toBeInTheDocument();
    expect(screen.getByText("Healthy")).toBeInTheDocument();
    expect(screen.getByText("Active")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "backend.apps.example.com" })).toHaveAttribute(
      "href",
      "https://backend.apps.example.com",
    );
  });

  it("shows a running process as starting until health converges", () => {
    const app = makeApp();
    app.platform_hostname = "backend.apps.example.com";
    app.desired_deployment_id = "deployment-1";
    app.desired_generation = 2;
    app.observed_generation = 2;
    app.runtime_status = AppRuntime_status.running;
    app.route_status = AppRoute_status.waiting_for_health;
    mocks.app = app;

    render(<AppDetailView organizationId="org-1" projectId="project-1" appId="app-1" />);

    expect(screen.getByText("Running")).toBeInTheDocument();
    expect(screen.getByText("Starting")).toBeInTheDocument();
    expect(screen.getByText("Waiting for health")).toBeInTheDocument();
    expect(screen.getByText("backend.apps.example.com")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "backend.apps.example.com" })).toBeNull();
    expect(screen.queryByText(/publicly available/i)).toBeNull();
  });

  it("keeps an unhealthy App out of the public route", () => {
    const app = makeApp();
    app.platform_hostname = "backend.apps.example.com";
    app.desired_deployment_id = "deployment-1";
    app.desired_generation = 3;
    app.observed_generation = 3;
    app.runtime_status = AppRuntime_status.running;
    app.health_status = AppHealth_status.unhealthy;
    app.route_status = AppRoute_status.waiting_for_health;
    mocks.app = app;

    render(<AppDetailView organizationId="org-1" projectId="project-1" appId="app-1" />);

    expect(screen.getByText("Running")).toBeInTheDocument();
    expect(screen.getByText("Unhealthy")).toBeInTheDocument();
    expect(screen.getByText("Waiting for health")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "backend.apps.example.com" })).toBeNull();
    expect(screen.queryByText(/publicly available/i)).toBeNull();
  });

  it("shows safe runtime failure details and preserves the last observed generation", () => {
    const app = makeApp();
    app.desired_deployment_id = "deployment-1";
    app.runtime_status = AppRuntime_status.degraded;
    app.runtime_error = "image verification failed";
    mocks.app = app;
    mocks.deployments = [makeDeployment()];
    render(
      <AppDetailView
        organizationId="org-1"
        projectId="project-1"
        appId="app-1"
      />,
    );

    expect(screen.getByRole("alert")).toHaveTextContent("image verification failed");
    expect(screen.getByText(/Generation 0 remains the last successfully applied state/)).toBeInTheDocument();
  });

  it("shows build failure details and never offers failed output for selection", () => {
    mocks.canManage = true;
    mocks.deployments = [
      makeDeployment({
        id: "deployment-failed",
        selected: false,
        status: AppDeploymentStatus.failed,
        build_status: AppDeploymentBuild_status.failed,
        error_message: "Dockerfile build failed",
        image_digest: null,
        image_archive_sha256: null,
        image_size_bytes: null,
      }),
    ];
    render(
      <AppDetailView
        organizationId="org-1"
        projectId="project-1"
        appId="app-1"
      />,
    );

    expect(screen.getByText("Failed")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Select as desired image" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Inspect" }));
    expect(screen.getByText("Dockerfile build failed")).toBeInTheDocument();
  });
});
