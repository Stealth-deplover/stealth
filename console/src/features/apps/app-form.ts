import {
  WorkloadSpecRequestHealth_checkProtocol,
  type components,
} from "@/api/generated/schema";
import type { StealthApp } from "@/api/types";

export type AppFormValues = {
  name: string;
  enabled: string;
  port: string;
  command_arguments: string;
  working_directory: string;
  health_protocol: string;
  health_path: string;
  cpu_millis: string;
  memory_bytes: string;
  pids_limit: string;
  stop_grace_period_seconds: string;
};

const emptyValues: AppFormValues = {
  name: "",
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
};

export function appFormValues(app?: StealthApp): AppFormValues {
  if (!app) return { ...emptyValues };
  return {
    name: app.name,
    enabled: String(app.enabled),
    port: String(app.workload.port),
    command_arguments: app.workload.command.join("\n"),
    working_directory: app.workload.working_directory ?? "",
    health_protocol: app.workload.health_check.protocol,
    health_path: app.workload.health_check.path ?? "",
    cpu_millis: String(app.workload.resources.cpu_millis),
    memory_bytes: String(app.workload.resources.memory_bytes),
    pids_limit: String(app.workload.resources.pids_limit),
    stop_grace_period_seconds: String(app.workload.stop_grace_period_seconds),
  };
}

export function createAppPayload(
  values: AppFormValues,
): components["schemas"]["CreateAppRequest"] {
  const name = validatedName(values.name);
  if (values.enabled !== "true" && values.enabled !== "false") {
    throw new Error("Choose whether this App should be enabled.");
  }
  return {
    name,
    enabled: values.enabled === "true",
    workload: workloadPayload(values),
  };
}

export function updateAppPayload(
  values: AppFormValues,
): components["schemas"]["UpdateAppRequest"] {
  const name = validatedName(values.name);
  if (values.enabled !== "true" && values.enabled !== "false") {
    throw new Error("Choose whether this App should be enabled.");
  }
  return {
    name,
    enabled: values.enabled === "true",
    workload: workloadPayload(values),
  };
}

function validatedName(raw: string) {
  const name = raw.trim().toLowerCase();
  if (!/^[a-z0-9][a-z0-9-]{1,62}$/.test(name)) {
    throw new Error(
      "App name must use 2 to 63 lowercase letters, numbers, or hyphens and start with a letter or number.",
    );
  }
  return name;
}

function workloadPayload(
  values: AppFormValues,
): components["schemas"]["WorkloadSpecRequest"] | undefined {
  const workload: components["schemas"]["WorkloadSpecRequest"] = {};
  const hasWorkloadField = () => Object.keys(workload).length > 0;

  if (values.port.trim()) {
    workload.port = integerInRange(values.port, "Port", 1, 65535);
  }

  const args = values.command_arguments
    .split(/\r\n|\n|\r/)
    .filter((argument) => argument.length > 0);
  if (args.length > 64)
    throw new Error("Command can contain at most 64 arguments.");
  let aggregateBytes = 0;
  for (const argument of args) {
    const argumentBytes = new TextEncoder().encode(argument).length;
    if (argument.includes("\0"))
      throw new Error("Command arguments cannot contain NUL.");
    if (argumentBytes > 4096)
      throw new Error("Each command argument must be 4096 bytes or fewer.");
    aggregateBytes += argumentBytes + 1;
  }
  if (aggregateBytes > 32 * 1024) {
    throw new Error("Command arguments must total 32 KiB or less.");
  }
  if (args.length) workload.command = args;

  if (values.working_directory !== "") {
    const directory = values.working_directory;
    if (
      !directory.startsWith("/") ||
      directory.length > 1024 ||
      directory.includes("\\") ||
      /[\u0000-\u001f\u007f]/.test(directory)
    ) {
      throw new Error("Working directory must be a safe absolute POSIX path.");
    }
    workload.working_directory = directory;
  }

  const healthProtocol = values.health_protocol;
  const healthPath = values.health_path;
  if (healthProtocol || healthPath) {
    if (healthProtocol !== "tcp" && healthProtocol !== "http") {
      throw new Error("Choose TCP or HTTP for the health check protocol.");
    }
    if (healthProtocol === "tcp") {
      if (healthPath) throw new Error("TCP health checks do not use a path.");
      workload.health_check = {
        protocol: WorkloadSpecRequestHealth_checkProtocol.tcp,
        path: null,
      };
    } else {
      validateHealthPath(healthPath);
      workload.health_check = {
        protocol: WorkloadSpecRequestHealth_checkProtocol.http,
        path: healthPath,
      };
    }
  }

  const resources: NonNullable<
    components["schemas"]["WorkloadSpecRequest"]["resources"]
  > = {};
  if (values.cpu_millis.trim()) {
    resources.cpu_millis = integerInRange(values.cpu_millis, "CPU", 50, 8000);
  }
  if (values.memory_bytes.trim()) {
    resources.memory_bytes = integerInRange(
      values.memory_bytes,
      "Memory",
      64 * 1024 * 1024,
      16 * 1024 * 1024 * 1024,
    );
  }
  if (values.pids_limit.trim()) {
    resources.pids_limit = integerInRange(
      values.pids_limit,
      "PIDs limit",
      16,
      2048,
    );
  }
  if (Object.keys(resources).length) workload.resources = resources;

  if (values.stop_grace_period_seconds.trim()) {
    workload.stop_grace_period_seconds = integerInRange(
      values.stop_grace_period_seconds,
      "Stop grace period",
      1,
      120,
    );
  }

  return hasWorkloadField() ? workload : undefined;
}

function integerInRange(
  value: string,
  label: string,
  min: number,
  max: number,
) {
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed < min || parsed > max) {
    throw new Error(`${label} must be an integer between ${min} and ${max}.`);
  }
  return parsed;
}

function validateHealthPath(value: string) {
  if (
    !value.startsWith("/") ||
    value.startsWith("//") ||
    value.length > 2048 ||
    /[\\?#\u0000-\u001f\u007f]/.test(value)
  ) {
    throw new Error("HTTP health path must be a local path beginning with /.");
  }
}
