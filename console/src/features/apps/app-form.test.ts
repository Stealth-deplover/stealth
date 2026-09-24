import { describe, expect, it } from "vitest";
import type { StealthApp } from "@/api/types";
import { AppRuntime_status, WorkloadHealthCheckProtocol } from "@/api/generated/schema";
import {
  appFormValues,
  createAppPayload,
  updateAppPayload,
  type AppFormValues,
} from "@/features/apps/app-form";

function values(overrides: Partial<AppFormValues> = {}): AppFormValues {
  return {
    name: "backend",
    enabled: "true",
    port: "",
    command_arguments: "",
    working_directory: "",
    health_protocol: "",
    health_path: "",
    cpu_millis: "",
    memory_bytes: "",
    pids_limit: "",
    stop_grace_period_seconds: "",
    ...overrides,
  };
}

describe("App configuration form", () => {
  it("leaves WorkloadSpec defaults to the shared API capability", () => {
    expect(createAppPayload(values())).toEqual({
      name: "backend",
      enabled: true,
      workload: undefined,
    });
  });

  it("maps argument-per-line input to an exec argument array without shell parsing", () => {
    const payload = createAppPayload(
      values({
        command_arguments: './server\n--message\nhello "world"',
      }),
    );
    expect(payload.workload?.command).toEqual([
      "./server",
      "--message",
      'hello "world"',
    ]);
  });

  it("validates typed runtime bounds and health path semantics", () => {
    expect(() => createAppPayload(values({ port: "65536" }))).toThrow(
      "Port must be an integer between 1 and 65535.",
    );
    expect(() =>
      createAppPayload(
        values({ health_protocol: "http", health_path: "https://outside.test/health" }),
      ),
    ).toThrow("HTTP health path must be a local path beginning with /.");
    expect(() => createAppPayload(values({ cpu_millis: "49" }))).toThrow(
      "CPU must be an integer between 50 and 8000.",
    );
    expect(() => createAppPayload(values({ working_directory: "relative" }))).toThrow(
      "Working directory must be a safe absolute POSIX path.",
    );
  });

  it("requires an HTTP path and keeps TCP path null", () => {
    expect(() => createAppPayload(values({ health_protocol: "http" }))).toThrow(
      "HTTP health path must be a local path beginning with /.",
    );
    expect(
      createAppPayload(values({ health_protocol: "tcp" })).workload?.health_check,
    ).toEqual({ protocol: "tcp", path: null });
  });

  it("round-trips existing settings into the edit form", () => {
    const app: StealthApp = {
      id: "app-1",
      project_id: "project-1",
      name: "backend",
      enabled: false,
      platform_hostname: "backend.apps.example.com",
      desired_deployment_id: null,
      workload: {
        schema_version: "v1",
        port: 9000,
        command: ["./server", "--flag", "a b"],
        working_directory: "/srv/app",
        health_check: {
          protocol: WorkloadHealthCheckProtocol.http,
          path: "/healthz",
          interval_seconds: 10,
          timeout_seconds: 2,
          initial_delay_seconds: 5,
          failure_threshold: 3,
        },
        resources: { cpu_millis: 700, memory_bytes: 1073741824, pids_limit: 128 },
        stop_grace_period_seconds: 20,
        restart_policy: "always",
      },
      workload_spec_sha256: "a".repeat(64),
      desired_generation: 2,
      observed_generation: 0,
      runtime_status: AppRuntime_status.not_deployed,
      runtime_error: null,
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    };
    const form = appFormValues(app);
    expect(form.command_arguments).toBe("./server\n--flag\na b");
    expect(updateAppPayload(form).workload?.command).toEqual([
      "./server",
      "--flag",
      "a b",
    ]);
    expect(updateAppPayload(form).enabled).toBe(false);
  });
});
