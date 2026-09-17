import { z } from "zod";
import type { components } from "@/api/generated/schema";

export const configSchema = z.object({
  instance_name: z.string().trim().min(1, "Give this instance a name."),
  public_url: z
    .string()
    .trim()
    .url("Enter an absolute HTTP(S) URL without a path.")
    .refine(
      (value) => !/[?#]/.test(value),
      "URL cannot contain a query or fragment.",
    ),
  network_mode: z.enum([
    "cloudflare_tunnel",
    "public_ip",
    "reverse_proxy",
    "local_only",
  ]),
  hostname: z.string().trim(),
  database_mode: z.enum(["bundled", "external"]),
  database_url: z.string().trim(),
  redis_mode: z.enum(["bundled", "external"]),
  redis_url: z.string().trim(),
  storage_mode: z.enum(["local", "s3"]),
  storage_s3_endpoint: z.string().trim(),
  storage_s3_region: z.string().trim(),
  storage_s3_bucket: z.string().trim(),
  storage_s3_access_key: z.string().trim(),
  storage_s3_secret_key: z.string(),
  storage_s3_use_ssl: z.boolean(),
  storage_s3_path_style: z.boolean(),
  storage_s3_prefix: z.string().trim(),
});

export const manualGitHubSchema = z.object({
  client_id: z.string().trim().min(1, "Client ID is required."),
  client_secret: z.string().min(1, "Client secret is required."),
  private_key: z.string().min(1, "Private key is required."),
  webhook_secret: z.string(),
});

export const setupCodeSchema = z
  .string()
  .trim()
  .toUpperCase()
  .regex(
    /^STEALTH-[A-HJ-NP-Z2-9]{4}-[A-HJ-NP-Z2-9]{4}-[A-HJ-NP-Z2-9]{4}$/,
    "Enter the setup code shown by the CLI.",
  );

export type ConfigValues = z.infer<typeof configSchema>;
export type ManualGitHubValues = z.infer<typeof manualGitHubSchema>;
export type SetupState = components["schemas"]["SetupState"];
export type SetupStep =
  | "welcome"
  | "instance"
  | "github"
  | "networking"
  | "data"
  | "storage"
  | "review"
  | "install";

export const setupSteps: Array<{
  id: SetupStep;
  label: string;
  short: string;
}> = [
  { id: "welcome", label: "Welcome", short: "Start" },
  { id: "instance", label: "Instance", short: "01" },
  { id: "github", label: "GitHub", short: "02" },
  { id: "networking", label: "Networking", short: "03" },
  { id: "data", label: "Database & Redis", short: "04" },
  { id: "storage", label: "Storage", short: "05" },
  { id: "review", label: "Review", short: "06" },
  { id: "install", label: "Install", short: "07" },
];

export const installationSteps = [
  "Configuration and secrets",
  "Release images",
  "PostgreSQL and Redis",
  "Database migrations",
  "API, Worker, Console, and Proxy",
  "Health and readiness verification",
] as const;

export type InstallationStepState =
  "complete" | "current" | "pending" | "failed";

const installationStepAliases: Record<string, number> = {
  "Preparing installation": -1,
  "Cloudflare Tunnel health": installationSteps.length,
  Handoff: installationSteps.length,
  Cleanup: installationSteps.length,
};

export function installationStepState(
  phase: string | undefined,
  currentStep: string | undefined,
  label: (typeof installationSteps)[number],
): InstallationStepState {
  if (phase === "complete" || phase === "handoff") return "complete";

  const currentIndex = currentStep
    ? (installationStepAliases[currentStep] ??
      installationSteps.indexOf(
        currentStep as (typeof installationSteps)[number],
      ))
    : -1;
  const stepIndex = installationSteps.indexOf(label);

  if (currentIndex < 0) return "pending";
  if (stepIndex < currentIndex) return "complete";
  if (stepIndex > currentIndex) return "pending";
  return phase === "failed" ? "failed" : "current";
}

export const defaultConfig: ConfigValues = {
  instance_name: "Stealth",
  public_url: "http://localhost:8081",
  network_mode: "cloudflare_tunnel",
  hostname: "",
  database_mode: "bundled",
  database_url: "",
  redis_mode: "bundled",
  redis_url: "",
  storage_mode: "local",
  storage_s3_endpoint: "",
  storage_s3_region: "us-east-1",
  storage_s3_bucket: "",
  storage_s3_access_key: "",
  storage_s3_secret_key: "",
  storage_s3_use_ssl: true,
  storage_s3_path_style: true,
  storage_s3_prefix: "",
};

export function configFromState(state: SetupState | undefined): ConfigValues {
  const draft = state?.draft;
  return {
    ...defaultConfig,
    instance_name: draft?.instance_name ?? defaultConfig.instance_name,
    public_url: draft?.public_url ?? defaultConfig.public_url,
    network_mode: draft?.network_mode ?? defaultConfig.network_mode,
    hostname: draft?.hostname ?? "",
    database_mode: draft?.database_mode ?? defaultConfig.database_mode,
    redis_mode: draft?.redis_mode ?? defaultConfig.redis_mode,
    storage_mode: draft?.storage_mode ?? defaultConfig.storage_mode,
    storage_s3_endpoint: draft?.storage_s3_endpoint ?? "",
    storage_s3_region:
      draft?.storage_s3_region ?? defaultConfig.storage_s3_region,
    storage_s3_bucket: draft?.storage_s3_bucket ?? "",
    storage_s3_use_ssl:
      draft?.storage_s3_use_ssl ?? defaultConfig.storage_s3_use_ssl,
    storage_s3_path_style:
      draft?.storage_s3_path_style ?? defaultConfig.storage_s3_path_style,
    storage_s3_prefix: draft?.storage_s3_prefix ?? "",
  } as ConfigValues;
}

export function toSetupRequest(
  values: ConfigValues,
): components["schemas"]["SetupConfigRequest"] {
  return {
    instance_name: values.instance_name,
    public_url: values.public_url,
    network_mode:
      values.network_mode as components["schemas"]["SetupConfigRequest"]["network_mode"],
    hostname: values.hostname || undefined,
    database_mode:
      values.database_mode as components["schemas"]["SetupConfigRequest"]["database_mode"],
    database_url: values.database_url || undefined,
    redis_mode:
      values.redis_mode as components["schemas"]["SetupConfigRequest"]["redis_mode"],
    redis_url: values.redis_url || undefined,
    storage_mode:
      values.storage_mode as components["schemas"]["SetupConfigRequest"]["storage_mode"],
    storage_s3_endpoint: values.storage_s3_endpoint || undefined,
    storage_s3_region: values.storage_s3_region || undefined,
    storage_s3_bucket: values.storage_s3_bucket || undefined,
    storage_s3_access_key: values.storage_s3_access_key || undefined,
    storage_s3_secret_key: values.storage_s3_secret_key || undefined,
    storage_s3_use_ssl: values.storage_s3_use_ssl,
    storage_s3_path_style: values.storage_s3_path_style,
    storage_s3_prefix: values.storage_s3_prefix || undefined,
  };
}
