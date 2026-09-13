"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { useForm, useWatch } from "react-hook-form";
import { z } from "zod";
import { zodResolver } from "@hookform/resolvers/zod";
import {
  ArrowLeft,
  ArrowRight,
  Check,
  CheckCircle2,
  CircleAlert,
  Cloud,
  Copy,
  Database,
  ExternalLink,
  Github,
  HardDrive,
  KeyRound,
  Loader2,
  Network,
  RefreshCw,
  ServerCog,
  ShieldCheck,
  Terminal,
  Wifi,
  XCircle,
} from "lucide-react";
import { ApiError, apiUrl } from "@/api/client";
import {
  usePollGitHubDeviceFlow,
  useSaveSetupCloudflareToken,
  useSaveSetupConfig,
  useSaveSetupGitHubManual,
  useStartSetupCloudflareOAuth,
  useStartSetupGitHubManifest,
  useStartSetupInstall,
  useTestSetupDatabase,
  useTestSetupRedis,
  useTestSetupStorage,
  useVerifyBootstrapCode,
  useStartGitHubDeviceFlow,
  useIssueSetupHandoffToken,
  useCreateSetupCloudflareTunnel,
} from "@/api/mutations";
import {
  useSetupCloudflareAccounts,
  useSetupCloudflareZones,
  useSetupPreflight,
  useSetupStatus,
} from "@/api/queries";
import type { components } from "@/api/generated/schema";
import { errorMessage } from "@/components/feedback/error-state";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

const configSchema = z.object({
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

const manualGitHubSchema = z.object({
  client_id: z.string().trim().min(1, "Client ID is required."),
  client_secret: z.string().min(1, "Client secret is required."),
  private_key: z.string().min(1, "Private key is required."),
  webhook_secret: z.string(),
});

const setupCodeSchema = z
  .string()
  .trim()
  .toUpperCase()
  .regex(
    /^STEALTH-[A-HJ-NP-Z2-9]{4}-[A-HJ-NP-Z2-9]{4}-[A-HJ-NP-Z2-9]{4}$/,
    "Enter the setup code shown by the CLI.",
  );

type ConfigValues = z.infer<typeof configSchema>;
type ManualGitHubValues = z.infer<typeof manualGitHubSchema>;
type SetupState = components["schemas"]["SetupState"];
type SetupStep =
  | "welcome"
  | "instance"
  | "github"
  | "networking"
  | "data"
  | "storage"
  | "review"
  | "install";

const setupSteps: Array<{ id: SetupStep; label: string; short: string }> = [
  { id: "welcome", label: "Welcome", short: "Start" },
  { id: "instance", label: "Instance", short: "01" },
  { id: "github", label: "GitHub", short: "02" },
  { id: "networking", label: "Networking", short: "03" },
  { id: "data", label: "Database & Redis", short: "04" },
  { id: "storage", label: "Storage", short: "05" },
  { id: "review", label: "Review", short: "06" },
  { id: "install", label: "Install", short: "07" },
];

const defaultConfig: ConfigValues = {
  instance_name: "Stealth",
  public_url: "http://127.0.0.1:8081",
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

function configFromState(state: SetupState | undefined): ConfigValues {
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

function toSetupRequest(
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

function fieldError(message: string | undefined) {
  return message ? <p className="text-xs text-rose-300">{message}</p> : null;
}

function safeError(error: unknown) {
  if (error instanceof ApiError) {
    if (error.code === "github_not_configured") {
      return "GitHub App setup is unavailable. Use the manual App option or configure the setup service.";
    }
    if (error.code === "handoff_unavailable") {
      return "The production session handoff is not ready yet. Keep this setup window open and retry.";
    }
  }
  return errorMessage(error);
}

function BrandMark() {
  return (
    <div className="flex items-center gap-2.5">
      <span className="flex size-9 items-center justify-center rounded-xl border border-cyan-300/30 bg-cyan-300/10 font-black text-cyan-200">
        S
      </span>
      <span className="text-sm font-semibold text-white">Stealth</span>
    </div>
  );
}

function ChoiceCard({
  active,
  icon: Icon,
  title,
  description,
  onClick,
}: {
  active: boolean;
  icon: typeof Cloud;
  title: string;
  description: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={`flex min-h-28 w-full items-start gap-3 rounded-xl border p-4 text-left transition-colors focus-visible:ring-2 focus-visible:ring-cyan-300/40 ${active ? "border-cyan-300/60 bg-cyan-300/[0.08]" : "border-stealth-border bg-stealth-panel hover:border-slate-500"}`}
    >
      <Icon
        className={`mt-0.5 size-5 shrink-0 ${active ? "text-cyan-300" : "text-slate-500"}`}
      />
      <span>
        <span className="block text-sm font-medium text-white">{title}</span>
        <span className="mt-1 block text-xs leading-5 text-slate-500">
          {description}
        </span>
      </span>
      {active ? (
        <Check className="ml-auto size-4 shrink-0 text-cyan-300" />
      ) : null}
    </button>
  );
}

function StatusPill({
  status,
  children,
}: {
  status: "ready" | "pending" | "warning" | "error";
  children: React.ReactNode;
}) {
  const styles = {
    ready: "border-emerald-300/25 bg-emerald-300/10 text-emerald-200",
    pending: "border-cyan-300/25 bg-cyan-300/10 text-cyan-200",
    warning: "border-amber-300/25 bg-amber-300/10 text-amber-200",
    error: "border-rose-300/25 bg-rose-300/10 text-rose-200",
  }[status];
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full border px-2 py-1 text-[11px] font-medium ${styles}`}
    >
      <span className="size-1.5 rounded-full bg-current" />
      {children}
    </span>
  );
}

function ActionError({ error }: { error: unknown }) {
  if (!error) return null;
  return (
    <div
      className="flex items-start gap-2 rounded-lg border border-rose-300/20 bg-rose-400/[0.08] px-3 py-2.5 text-xs leading-5 text-rose-200"
      role="alert"
    >
      <CircleAlert className="mt-0.5 size-4 shrink-0" />
      <span>{safeError(error)}</span>
    </div>
  );
}

function Field({
  id,
  label,
  hint,
  error,
  children,
}: {
  id: string;
  label: string;
  hint?: string;
  error?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {hint ? <p className="text-xs leading-5 text-slate-600">{hint}</p> : null}
      {fieldError(error)}
    </div>
  );
}

function StageHeader({
  eyebrow,
  title,
  description,
}: {
  eyebrow: string;
  title: string;
  description: string;
}) {
  return (
    <header className="mb-7">
      <p className="mb-2 text-xs font-medium uppercase tracking-[0.2em] text-cyan-300">
        {eyebrow}
      </p>
      <h1 className="text-2xl font-semibold tracking-[-0.03em] text-white sm:text-3xl">
        {title}
      </h1>
      <p className="mt-3 max-w-2xl text-sm leading-6 text-slate-400">
        {description}
      </p>
    </header>
  );
}

function StageActions({
  back,
  next,
  nextLabel = "Continue",
  nextDisabled,
  pending,
}: {
  back?: () => void;
  next?: () => void;
  nextLabel?: string;
  nextDisabled?: boolean;
  pending?: boolean;
}) {
  return (
    <div className="mt-8 flex flex-col-reverse gap-3 border-t border-stealth-border pt-5 sm:flex-row sm:items-center sm:justify-between">
      {back ? (
        <Button type="button" variant="ghost" onClick={back}>
          <ArrowLeft className="size-4" /> Back
        </Button>
      ) : (
        <span />
      )}
      {next ? (
        <Button type="button" onClick={next} disabled={nextDisabled || pending}>
          {pending ? <Loader2 className="size-4 animate-spin" /> : null}
          {pending ? "Saving" : nextLabel}
          {!pending ? <ArrowRight className="size-4" /> : null}
        </Button>
      ) : null}
    </div>
  );
}

function ReviewRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-1 border-b border-stealth-border/70 py-3 last:border-b-0 sm:flex-row sm:items-center sm:justify-between sm:gap-6">
      <dt className="text-xs uppercase tracking-[0.12em] text-slate-600">
        {label}
      </dt>
      <dd className="break-all text-sm text-slate-200 sm:text-right">
        {value}
      </dd>
    </div>
  );
}

function ProductionLink({ publicURL }: { publicURL?: string }) {
  return (
    <div className="flex flex-col gap-4 rounded-xl border border-emerald-300/20 bg-emerald-300/[0.06] p-5">
      <div className="flex items-start gap-3">
        <CheckCircle2 className="mt-0.5 size-5 shrink-0 text-emerald-300" />
        <div>
          <p className="text-sm font-medium text-emerald-100">
            Production is ready
          </p>
          <p className="mt-1 text-xs leading-5 text-emerald-200/70">
            The setup route is sealed. Open the production Console to sign in.
          </p>
        </div>
      </div>
      <Button asChild variant="secondary" className="w-full sm:w-auto">
        <Link href={publicURL ? `${publicURL}/login` : "/login"}>
          Open production Console <ExternalLink className="size-4" />
        </Link>
      </Button>
    </div>
  );
}

function submitHandoff(publicURL: string, token: string) {
  const form = document.createElement("form");
  form.method = "post";
  form.action = `${publicURL.replace(/\/+$/, "")}/v1/setup/handoff`;
  form.style.display = "none";
  const input = document.createElement("input");
  input.type = "hidden";
  input.name = "token";
  input.value = token;
  form.appendChild(input);
  document.body.appendChild(form);
  form.submit();
}

export function BrowserSetupView() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const setupStatus = useSetupStatus();
  const state = setupStatus.data?.state;
  const callbackStatus = [
    searchParams.get("github"),
    searchParams.get("cloudflare"),
  ]
    .filter(Boolean)
    .join(":");
  const configForm = useForm<ConfigValues>({
    resolver: zodResolver(configSchema),
    defaultValues: defaultConfig,
    mode: "onTouched",
  });
  const manualForm = useForm<ManualGitHubValues>({
    resolver: zodResolver(manualGitHubSchema),
    defaultValues: {
      client_id: "",
      client_secret: "",
      private_key: "",
      webhook_secret: "",
    },
    mode: "onTouched",
  });
  const [step, setStep] = useState<SetupStep>("welcome");
  const [setupCode, setSetupCode] = useState("");
  const [setupVerified, setSetupVerified] = useState(false);
  const [providerMode, setProviderMode] = useState<"manifest" | "manual">(
    "manifest",
  );
  const [device, setDevice] =
    useState<components["schemas"]["GitHubDeviceResponse"]>();
  const [authorizationSessionID, setAuthorizationSessionID] = useState("");
  const [pollDelay, setPollDelay] = useState(5_000);
  const [pollError, setPollError] = useState<unknown>();
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">(
    "idle",
  );
  const [cloudflareToken, setCloudflareToken] = useState("");
  const [cloudflareAccountID, setCloudflareAccountID] = useState("");
  const [cloudflareZoneID, setCloudflareZoneID] = useState("");
  const [handoffToken, setHandoffToken] = useState("");
  const [installState, setInstallState] = useState<SetupState>();
  const [sseConnected, setSSEConnected] = useState(false);
  const [actionError, setActionError] = useState<unknown>();
  const [notice, setNotice] = useState("");
  const hydrated = useRef(false);
  const callbackHandled = useRef("");
  const pollDevice = usePollGitHubDeviceFlow();
  const pollRef = useRef(pollDevice.mutateAsync);
  const handoffSubmitted = useRef(false);
  const verifyCode = useVerifyBootstrapCode();
  const startDevice = useStartGitHubDeviceFlow();
  const saveConfig = useSaveSetupConfig();
  const startManifest = useStartSetupGitHubManifest();
  const saveManual = useSaveSetupGitHubManual();
  const startCloudflareOAuth = useStartSetupCloudflareOAuth();
  const saveCloudflareToken = useSaveSetupCloudflareToken();
  const createCloudflareTunnel = useCreateSetupCloudflareTunnel();
  const testDatabase = useTestSetupDatabase();
  const testRedis = useTestSetupRedis();
  const testStorage = useTestSetupStorage();
  const startInstall = useStartSetupInstall();
  const issueHandoff = useIssueSetupHandoffToken();
  const watchedConfig = useWatch({ control: configForm.control });
  const networkMode = watchedConfig.network_mode ?? defaultConfig.network_mode;
  const databaseMode =
    watchedConfig.database_mode ?? defaultConfig.database_mode;
  const redisMode = watchedConfig.redis_mode ?? defaultConfig.redis_mode;
  const storageMode = watchedConfig.storage_mode ?? defaultConfig.storage_mode;
  const selectedAccount =
    cloudflareAccountID || state?.draft.cloudflare_account_id || "";
  const selectedZone =
    cloudflareZoneID || state?.draft.cloudflare_zone_id || "";
  const ownerConfirmed = Boolean(
    setupStatus.data && !setupStatus.data.setup_required,
  );
  const setupAccess =
    setupVerified ||
    ownerConfirmed ||
    Boolean(state?.github.authorization_session);
  const preflight = useSetupPreflight(setupAccess);
  const accounts = useSetupCloudflareAccounts(
    setupAccess && Boolean(state?.cloudflare.connected),
  );
  const zones = useSetupCloudflareZones(
    selectedAccount,
    setupAccess && Boolean(state?.cloudflare.connected),
  );
  const activeStep =
    state?.phase === "installing" ||
    state?.phase === "failed" ||
    state?.phase === "handoff"
      ? "install"
      : step;

  useEffect(() => {
    pollRef.current = pollDevice.mutateAsync;
  }, [pollDevice.mutateAsync]);

  useEffect(() => {
    if (!state || hydrated.current) return;
    configForm.reset(configFromState(state));
    setAuthorizationSessionID(state.github.authorization_session ?? "");
    setCloudflareAccountID(state.draft.cloudflare_account_id ?? "");
    setCloudflareZoneID(state.draft.cloudflare_zone_id ?? "");
    setSetupVerified(Boolean(state.github.authorization_session));
    hydrated.current = true;
  }, [configForm, state]);

  useEffect(() => {
    const callback = callbackStatus;
    if (!callback || callbackHandled.current === callback) return;
    callbackHandled.current = callback;
    void setupStatus.refetch();
    router.replace("/setup", { scroll: false });
  }, [callbackStatus, router, setupStatus]);

  useEffect(() => {
    if (activeStep !== "install") return;
    const source = new EventSource(apiUrl("/v1/setup/install/events"), {
      withCredentials: true,
    });
    source.onopen = () => setSSEConnected(true);
    source.addEventListener("snapshot", (event) => {
      try {
        setInstallState(JSON.parse(event.data) as SetupState);
      } catch {
        setActionError(
          new Error("Install progress returned an invalid snapshot."),
        );
      }
    });
    source.addEventListener("progress", (event) => {
      try {
        const progress = JSON.parse(event.data) as {
          step?: string;
          error?: string;
        };
        setInstallState((current) =>
          current
            ? {
                ...current,
                step: progress.step,
                error_message: progress.error,
              }
            : current,
        );
      } catch {
        setActionError(
          new Error("Install progress returned an invalid event."),
        );
      }
    });
    source.onerror = () => setSSEConnected(false);
    return () => source.close();
  }, [activeStep]);

  useEffect(() => {
    if (activeStep !== "install") return;
    const timer = window.setInterval(() => void setupStatus.refetch(), 2_000);
    return () => window.clearInterval(timer);
  }, [activeStep, setupStatus]);

  useEffect(() => {
    if (
      activeStep !== "install" ||
      !state ||
      (state.phase !== "complete" && state.phase !== "handoff") ||
      handoffSubmitted.current
    ) {
      return;
    }
    const publicURL =
      state.draft.public_url ?? configForm.getValues("public_url");
    if (!publicURL) return;
    handoffSubmitted.current = true;
    if (handoffToken) {
      submitHandoff(publicURL, handoffToken);
      return;
    }
    void issueHandoff
      .mutateAsync()
      .then((result) => {
        if (result?.token) submitHandoff(publicURL, result.token);
        else
          setActionError(
            new Error("The production session handoff was empty."),
          );
      })
      .catch((error) => {
        handoffSubmitted.current = false;
        setActionError(error);
      });
  }, [activeStep, configForm, handoffToken, issueHandoff, state]);

  const persistConfig = useCallback(
    async (values: ConfigValues) => {
      const saved = await saveConfig.mutateAsync(toSetupRequest(values));
      await setupStatus.refetch();
      return saved;
    },
    [saveConfig, setupStatus],
  );

  const moveTo = (next: SetupStep) => {
    setActionError(undefined);
    setNotice("");
    setStep(next);
  };

  const verifySetupCode = async () => {
    const parsed = setupCodeSchema.safeParse(setupCode);
    if (!parsed.success) {
      setActionError(
        new Error(
          parsed.error.issues[0]?.message ??
            "Enter the setup code shown by the CLI.",
        ),
      );
      return;
    }
    setActionError(undefined);
    try {
      const result = await verifyCode.mutateAsync({ setup_code: parsed.data });
      if (!result) return;
      setSetupCode(parsed.data);
      setAuthorizationSessionID(result.authorization_session_id);
      setSetupVerified(true);
      await preflight.refetch();
    } catch (error) {
      setActionError(error);
    }
  };

  const beginDeviceFlow = async () => {
    const parsed = setupCodeSchema.safeParse(setupCode);
    if (!parsed.success) {
      setActionError(
        new Error("Enter the setup code again to confirm the first owner."),
      );
      return;
    }
    setActionError(undefined);
    try {
      let sessionID = authorizationSessionID;
      if (!sessionID) {
        const verified = await verifyCode.mutateAsync({
          setup_code: parsed.data,
        });
        if (!verified) return;
        sessionID = verified.authorization_session_id;
        setAuthorizationSessionID(sessionID);
        setSetupVerified(true);
      }
      const result = await startDevice.mutateAsync({
        authorization_session_id: sessionID,
        setup_code: parsed.data,
      });
      if (!result) return;
      setDevice(result);
      setPollDelay(Math.max(1, result.interval_seconds) * 1_000);
      setPollError(undefined);
      setCopyState("idle");
    } catch (error) {
      setActionError(error);
    }
  };

  useEffect(() => {
    if (!device || !authorizationSessionID) return;
    let cancelled = false;
    const timer = window.setTimeout(async () => {
      if (new Date(device.expires_at).getTime() <= Date.now()) {
        setPollError(
          new Error("GitHub authorization expired. Start it again."),
        );
        setDevice(undefined);
        return;
      }
      try {
        const result = await pollRef.current({
          authorization_session_id: authorizationSessionID,
        });
        if (cancelled || !result) return;
        if (result.status === "complete") {
          let ticket = result.handoff_token ?? "";
          if (!ticket) {
            const handoff = await issueHandoff.mutateAsync();
            ticket = handoff?.token ?? "";
          }
          setHandoffToken(ticket);
          setDevice(undefined);
          setNotice("GitHub verified. The Instance Owner is ready.");
          await setupStatus.refetch();
          return;
        }
        setPollError(undefined);
        setPollDelay(Math.max(1, result.retry_after_seconds ?? 5) * 1_000);
      } catch (error) {
        if (cancelled) return;
        setPollError(error);
        setPollDelay(5_000);
      }
    }, pollDelay);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [authorizationSessionID, device, issueHandoff, pollDelay, setupStatus]);

  const saveManualGitHub = manualForm.handleSubmit(async (values) => {
    setActionError(undefined);
    try {
      await saveManual.mutateAsync(values);
      await setupStatus.refetch();
      setNotice("GitHub App credentials saved in encrypted setup state.");
    } catch (error) {
      setActionError(error);
    }
  });

  const connectCloudflareToken = async () => {
    if (!cloudflareToken.trim()) {
      setActionError(new Error("Enter a Cloudflare API token."));
      return;
    }
    setActionError(undefined);
    try {
      await saveCloudflareToken.mutateAsync({
        api_token: cloudflareToken.trim(),
      });
      setCloudflareToken("");
      await setupStatus.refetch();
    } catch (error) {
      setActionError(error);
    }
  };

  const startCloudflareAuthorization = async () => {
    setActionError(undefined);
    try {
      const result = await startCloudflareOAuth.mutateAsync();
      if (result?.authorization_url)
        window.location.assign(result.authorization_url);
    } catch (error) {
      setActionError(error);
    }
  };

  const createTunnel = async () => {
    const values = configForm.getValues();
    if (!selectedAccount || !selectedZone || !values.hostname.trim()) {
      setActionError(new Error("Choose an account, zone, and hostname first."));
      return;
    }
    setActionError(undefined);
    try {
      await persistConfig(values);
      const result = await createCloudflareTunnel.mutateAsync({
        account_id: selectedAccount,
        zone_id: selectedZone,
        hostname: values.hostname.trim(),
      });
      if (result?.draft.public_url)
        configForm.setValue("public_url", result.draft.public_url);
      await setupStatus.refetch();
    } catch (error) {
      setActionError(error);
    }
  };

  const testDatabaseConnection = async () => {
    setActionError(undefined);
    try {
      await persistConfig(configForm.getValues());
      await testDatabase.mutateAsync({
        url: configForm.getValues("database_url") || undefined,
      });
      await setupStatus.refetch();
    } catch (error) {
      setActionError(error);
    }
  };

  const testRedisConnection = async () => {
    setActionError(undefined);
    try {
      await persistConfig(configForm.getValues());
      await testRedis.mutateAsync({
        url: configForm.getValues("redis_url") || undefined,
      });
      await setupStatus.refetch();
    } catch (error) {
      setActionError(error);
    }
  };

  const testStorageConnection = async () => {
    setActionError(undefined);
    try {
      const values = configForm.getValues();
      await persistConfig(values);
      await testStorage.mutateAsync({
        endpoint: values.storage_s3_endpoint || undefined,
        region: values.storage_s3_region || undefined,
        bucket: values.storage_s3_bucket || undefined,
        access_key: values.storage_s3_access_key || undefined,
        secret_key: values.storage_s3_secret_key || undefined,
        use_ssl: values.storage_s3_use_ssl,
        path_style: values.storage_s3_path_style,
      });
      await setupStatus.refetch();
    } catch (error) {
      setActionError(error);
    }
  };

  const startProductionInstall = async () => {
    setActionError(undefined);
    handoffSubmitted.current = false;
    try {
      if (!ownerConfirmed) {
        throw new Error(
          "Finish GitHub first-owner verification before installing.",
        );
      }
      const ticket = await issueHandoff.mutateAsync();
      if (!ticket?.token)
        throw new Error("The production session handoff is not ready.");
      setHandoffToken(ticket.token);
      await persistConfig(configForm.getValues());
      const result = await startInstall.mutateAsync();
      if (result) setInstallState(result.state);
      moveTo("install");
    } catch (error) {
      setActionError(error);
    }
  };

  const retryProductionInstall = async () => {
    setActionError(undefined);
    handoffSubmitted.current = false;
    try {
      const ticket = await issueHandoff.mutateAsync();
      if (ticket?.token) setHandoffToken(ticket.token);
      const result = await startInstall.mutateAsync();
      if (result) setInstallState(result.state);
    } catch (error) {
      setActionError(error);
    }
  };

  const checks = preflight.data?.checks ?? [];
  const checksPass =
    checks.length > 0 &&
    checks.every((check) => !check.required || check.status === "pass");
  const cloudflareConnected = Boolean(state?.cloudflare.connected);
  const tunnelReady = Boolean(
    state?.draft.cloudflare_tunnel_id &&
    state.draft.cloudflare_record_id &&
    state.draft.hostname,
  );
  const dataReady =
    (databaseMode === "bundled" || Boolean(state?.draft.database_tested)) &&
    (redisMode === "bundled" || Boolean(state?.draft.redis_tested));
  const storageReady =
    storageMode === "local" || Boolean(state?.draft.storage_tested);
  const installViewState = installState ?? state;
  const currentIndex = setupSteps.findIndex((item) => item.id === activeStep);
  const callbackNotice = callbackStatus.includes("connected")
    ? "Connection saved. Continue when the status below is ready."
    : "";
  const callbackError =
    callbackStatus && !callbackStatus.includes("connected")
      ? new Error("The provider connection was not completed.")
      : undefined;
  const isPending =
    saveConfig.isPending ||
    saveManual.isPending ||
    startInstall.isPending ||
    issueHandoff.isPending;

  if (setupStatus.isPending) {
    return (
      <SetupShell currentStep="welcome">
        <LoadingPanel label="Checking setup service" />
      </SetupShell>
    );
  }

  if (setupStatus.error) {
    if (
      setupStatus.error instanceof ApiError &&
      setupStatus.error.status === 404
    ) {
      return (
        <SetupShell currentStep="welcome">
          <ProductionLink />
        </SetupShell>
      );
    }
    return (
      <SetupShell currentStep="welcome">
        <Card role="alert" className="border-rose-300/20 bg-rose-400/[0.04]">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-rose-200">
              <XCircle className="size-4" /> Setup service unavailable
            </CardTitle>
          </CardHeader>
          <CardContent className="flex flex-wrap items-center gap-3">
            <p className="text-sm text-slate-400">
              {safeError(setupStatus.error)}
            </p>
            <Button
              variant="outline"
              onClick={() => void setupStatus.refetch()}
            >
              <RefreshCw className="size-4" /> Retry
            </Button>
          </CardContent>
        </Card>
      </SetupShell>
    );
  }

  if (installViewState?.phase === "complete") {
    return (
      <SetupShell currentStep="install">
        <ProductionLink publicURL={installViewState.draft.public_url} />
      </SetupShell>
    );
  }

  const canOpenStep = (target: SetupStep) => {
    const targetIndex = setupSteps.findIndex((item) => item.id === target);
    return targetIndex <= currentIndex && activeStep !== "install";
  };

  return (
    <SetupShell currentStep={activeStep}>
      <div className="grid gap-7 lg:grid-cols-[220px_minmax(0,1fr)]">
        <nav aria-label="Setup progress" className="lg:pt-1">
          <div className="mb-3 flex items-center justify-between lg:block">
            <p className="text-xs font-medium uppercase tracking-[0.18em] text-slate-500">
              Setup progress
            </p>
            <StatusPill
              status={
                installViewState?.phase === "failed" ? "error" : "pending"
              }
            >
              {installViewState?.phase === "failed"
                ? "Needs attention"
                : "In progress"}
            </StatusPill>
          </div>
          <ol className="flex gap-2 overflow-x-auto pb-2 lg:block lg:space-y-1 lg:overflow-visible">
            {setupSteps.map((item, index) => {
              const active = item.id === activeStep;
              const available = canOpenStep(item.id) || active;
              return (
                <li key={item.id} className="shrink-0">
                  <button
                    type="button"
                    disabled={!available}
                    onClick={() => available && moveTo(item.id)}
                    className={`flex min-h-10 items-center gap-2 rounded-lg px-2.5 py-2 text-left text-xs transition-colors focus-visible:ring-2 focus-visible:ring-cyan-300/40 lg:w-full ${active ? "bg-cyan-300/[0.08] text-cyan-200" : available ? "text-slate-400 hover:bg-white/[0.04] hover:text-white" : "cursor-not-allowed text-slate-700"}`}
                  >
                    <span
                      className={`flex size-6 items-center justify-center rounded-full border font-mono text-[10px] ${active ? "border-cyan-300/60" : "border-stealth-border"}`}
                    >
                      {index === 0 ? "S" : item.short}
                    </span>
                    <span className="hidden lg:block">{item.label}</span>
                  </button>
                </li>
              );
            })}
          </ol>
        </nav>

        <section
          className="min-w-0 rounded-xl border border-stealth-border bg-stealth-panel/70 p-5 sm:p-7"
          aria-live="polite"
        >
          {notice || callbackNotice ? (
            <div
              className="mb-5 flex items-start gap-2 rounded-lg border border-cyan-300/20 bg-cyan-300/[0.06] px-3 py-2.5 text-xs leading-5 text-cyan-100"
              role="status"
            >
              <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-cyan-300" />
              <span>{notice || callbackNotice}</span>
            </div>
          ) : null}
          <ActionError error={actionError ?? callbackError} />

          {activeStep === "welcome" ? (
            <>
              <StageHeader
                eyebrow="Welcome"
                title="Set up your Stealth instance"
                description="The browser handles the reviewed configuration. Your terminal stays responsible for Docker and the temporary setup connection."
              />
              <div className="grid gap-4 sm:grid-cols-3">
                <InfoCard
                  icon={Terminal}
                  title="Local control"
                  description="The setup API is reachable only through your temporary setup URL."
                />
                <InfoCard
                  icon={ShieldCheck}
                  title="Server-side secrets"
                  description="Provider credentials stay encrypted and never enter the public state."
                />
                <InfoCard
                  icon={RefreshCw}
                  title="Resumable"
                  description="Refresh or reconnect without restarting the installation from scratch."
                />
              </div>
              <div className="mt-7 rounded-xl border border-stealth-border bg-black/10 p-5">
                <div className="flex items-start gap-3">
                  <KeyRound className="mt-0.5 size-5 shrink-0 text-cyan-300" />
                  <div>
                    <p className="text-sm font-medium text-white">
                      Verify this setup session
                    </p>
                    <p className="mt-1 text-xs leading-5 text-slate-500">
                      Enter the one-time code printed by `stealth install`. It
                      expires after 15 minutes and is not placed in the URL.
                    </p>
                  </div>
                </div>
                <div className="mt-5 flex flex-col gap-3 sm:flex-row sm:items-end">
                  <div className="flex-1 space-y-2">
                    <Label htmlFor="browser-setup-code">Setup code</Label>
                    <Input
                      id="browser-setup-code"
                      value={setupCode}
                      onChange={(event) =>
                        setSetupCode(event.target.value.toUpperCase())
                      }
                      placeholder="STEALTH-XXXX-XXXX-XXXX"
                      autoComplete="one-time-code"
                      spellCheck={false}
                      translate="no"
                      disabled={setupVerified}
                    />
                  </div>
                  <Button
                    type="button"
                    onClick={() => void verifySetupCode()}
                    disabled={setupVerified || verifyCode.isPending}
                  >
                    {verifyCode.isPending ? (
                      <Loader2 className="size-4 animate-spin" />
                    ) : (
                      <ShieldCheck className="size-4" />
                    )}
                    {setupVerified ? "Verified" : "Verify code"}
                  </Button>
                </div>
              </div>
              <div className="mt-7 rounded-xl border border-stealth-border p-5">
                <div className="flex items-center justify-between gap-4">
                  <div>
                    <p className="text-sm font-medium text-white">
                      System check
                    </p>
                    <p className="mt-1 text-xs text-slate-500">
                      Unlock the live dependency checks after verifying the
                      setup code.
                    </p>
                  </div>
                  {preflight.isPending ? (
                    <Loader2 className="size-5 animate-spin text-cyan-300" />
                  ) : null}
                </div>
                <div className="mt-4 space-y-2">
                  {checks.length === 0 ? (
                    <p className="text-sm text-slate-600">
                      System checks will appear here.
                    </p>
                  ) : (
                    checks.map((check) => (
                      <div
                        key={check.name}
                        className="flex items-start justify-between gap-4 rounded-lg bg-black/10 px-3 py-2.5"
                      >
                        <div>
                          <p className="text-sm text-slate-200">{check.name}</p>
                          <p className="mt-0.5 text-xs text-slate-600">
                            {check.detail}
                          </p>
                        </div>
                        <StatusPill
                          status={
                            check.status === "pass"
                              ? "ready"
                              : check.status === "warn"
                                ? "warning"
                                : "error"
                          }
                        >
                          {check.status === "pass"
                            ? "Ready"
                            : check.status === "warn"
                              ? "Review"
                              : "Blocked"}
                        </StatusPill>
                      </div>
                    ))
                  )}
                </div>
              </div>
              <StageActions
                next={() => moveTo("instance")}
                nextLabel="Start configuration"
                nextDisabled={!setupVerified || !checksPass}
              />
            </>
          ) : null}

          {activeStep === "instance" ? (
            <form
              onSubmit={configForm.handleSubmit(async (values) => {
                setActionError(undefined);
                try {
                  await persistConfig(values);
                  moveTo("github");
                } catch (error) {
                  setActionError(error);
                }
              })}
            >
              <StageHeader
                eyebrow="01 / Instance"
                title="Name and locate the instance"
                description="Choose the name shown to operators and the URL the production Console will use."
              />
              <div className="space-y-5">
                <Field
                  id="instance-name"
                  label="Instance name"
                  hint="This label stays inside your installation."
                >
                  <Input
                    id="instance-name"
                    {...configForm.register("instance_name")}
                  />
                  {fieldError(
                    configForm.formState.errors.instance_name?.message,
                  )}
                </Field>
                <Field
                  id="public-url"
                  label="Public Console URL"
                  hint="Use the final URL, without a path, query, or fragment."
                >
                  <Input
                    id="public-url"
                    type="url"
                    placeholder="https://console.example.com"
                    {...configForm.register("public_url")}
                  />
                  {fieldError(configForm.formState.errors.public_url?.message)}
                </Field>
              </div>
              <StageActions
                back={() => moveTo("welcome")}
                next={() =>
                  void configForm.handleSubmit(async (values) => {
                    try {
                      await persistConfig(values);
                      moveTo("github");
                    } catch (error) {
                      setActionError(error);
                    }
                  })()
                }
                nextLabel="Save and connect GitHub"
                pending={isPending}
              />
            </form>
          ) : null}

          {activeStep === "github" ? (
            <>
              <StageHeader
                eyebrow="02 / GitHub"
                title="Connect the owner identity"
                description="Connect a GitHub App for installation metadata, then verify the person who will become the first Instance Owner."
              />
              {state?.github.connected ? (
                <div className="flex items-start gap-3 rounded-xl border border-emerald-300/20 bg-emerald-300/[0.06] p-5">
                  <CheckCircle2 className="mt-0.5 size-5 shrink-0 text-emerald-300" />
                  <div>
                    <p className="text-sm font-medium text-emerald-100">
                      GitHub App connected
                    </p>
                    <p className="mt-1 text-xs leading-5 text-emerald-200/70">
                      The App credentials are held by the setup API. The Console
                      only sees the connection status.
                    </p>
                  </div>
                </div>
              ) : (
                <>
                  <div className="mb-5 flex gap-1 border-b border-stealth-border">
                    <button
                      type="button"
                      onClick={() => setProviderMode("manifest")}
                      className={`min-h-11 border-b-2 px-3 text-xs font-medium ${providerMode === "manifest" ? "border-cyan-300 text-cyan-200" : "border-transparent text-slate-500"}`}
                    >
                      GitHub Manifest
                    </button>
                    <button
                      type="button"
                      onClick={() => setProviderMode("manual")}
                      className={`min-h-11 border-b-2 px-3 text-xs font-medium ${providerMode === "manual" ? "border-cyan-300 text-cyan-200" : "border-transparent text-slate-500"}`}
                    >
                      Manual App
                    </button>
                  </div>
                  {providerMode === "manifest" ? (
                    <div className="rounded-xl border border-stealth-border p-5">
                      <div className="flex items-start gap-3">
                        <Github className="mt-0.5 size-5 shrink-0 text-slate-200" />
                        <div>
                          <p className="text-sm font-medium text-white">
                            Create a private GitHub App
                          </p>
                          <p className="mt-1 text-xs leading-5 text-slate-500">
                            GitHub opens its own registration page and returns
                            the App credentials to this setup service through a
                            one-time callback.
                          </p>
                        </div>
                      </div>
                      <Button
                        className="mt-5"
                        onClick={() =>
                          void startManifest
                            .mutateAsync()
                            .then(
                              (result) =>
                                result?.manifest_url &&
                                window.location.assign(result.manifest_url),
                            )
                            .catch(setActionError)
                        }
                        disabled={startManifest.isPending}
                      >
                        {startManifest.isPending ? (
                          <Loader2 className="size-4 animate-spin" />
                        ) : (
                          <Github className="size-4" />
                        )}
                        {startManifest.isPending
                          ? "Opening GitHub"
                          : "Continue with GitHub"}
                      </Button>
                    </div>
                  ) : (
                    <form onSubmit={saveManualGitHub} className="space-y-5">
                      <Field id="github-client-id" label="Client ID">
                        <Input
                          id="github-client-id"
                          autoComplete="off"
                          {...manualForm.register("client_id")}
                        />
                        {fieldError(
                          manualForm.formState.errors.client_id?.message,
                        )}
                      </Field>
                      <Field id="github-client-secret" label="Client secret">
                        <Input
                          id="github-client-secret"
                          type="password"
                          autoComplete="new-password"
                          {...manualForm.register("client_secret")}
                        />
                        {fieldError(
                          manualForm.formState.errors.client_secret?.message,
                        )}
                      </Field>
                      <Field
                        id="github-private-key"
                        label="Private key"
                        hint="Paste the complete PEM value. It is submitted only to the setup API."
                      >
                        <Textarea
                          id="github-private-key"
                          rows={8}
                          spellCheck={false}
                          {...manualForm.register("private_key")}
                        />
                        {fieldError(
                          manualForm.formState.errors.private_key?.message,
                        )}
                      </Field>
                      <Field
                        id="github-webhook-secret"
                        label="Webhook secret"
                        hint="Optional for first-run setup."
                      >
                        <Input
                          id="github-webhook-secret"
                          type="password"
                          autoComplete="new-password"
                          {...manualForm.register("webhook_secret")}
                        />
                      </Field>
                      <Button type="submit" disabled={saveManual.isPending}>
                        {saveManual.isPending ? (
                          <Loader2 className="size-4 animate-spin" />
                        ) : (
                          <ShieldCheck className="size-4" />
                        )}
                        {saveManual.isPending
                          ? "Saving securely"
                          : "Save GitHub App"}
                      </Button>
                    </form>
                  )}
                </>
              )}
              {state?.github.connected ? (
                <div className="mt-6 border-t border-stealth-border pt-6">
                  {ownerConfirmed ? (
                    <div className="flex items-start gap-3 rounded-xl border border-emerald-300/20 bg-emerald-300/[0.06] p-5">
                      <CheckCircle2 className="mt-0.5 size-5 shrink-0 text-emerald-300" />
                      <div>
                        <p className="text-sm font-medium text-emerald-100">
                          Instance Owner verified
                        </p>
                        <p className="mt-1 text-xs leading-5 text-emerald-200/70">
                          The first-owner bootstrap is sealed. The setup claim
                          remains scoped to this browser until production
                          handoff.
                        </p>
                      </div>
                    </div>
                  ) : device ? (
                    <DeviceFlow
                      device={device}
                      copyState={copyState}
                      onCopy={async () => {
                        try {
                          await navigator.clipboard.writeText(device.user_code);
                          setCopyState("copied");
                        } catch {
                          setCopyState("failed");
                        }
                      }}
                      pollError={pollError}
                      onCancel={() => setDevice(undefined)}
                    />
                  ) : (
                    <div className="rounded-xl border border-stealth-border p-5">
                      <p className="text-sm font-medium text-white">
                        Confirm first-owner identity
                      </p>
                      <p className="mt-1 text-xs leading-5 text-slate-500">
                        The server will show a GitHub device code. No password
                        is created for the Instance Owner.
                      </p>
                      <div className="mt-5 flex flex-col gap-3 sm:flex-row sm:items-end">
                        <div className="flex-1 space-y-2">
                          <Label htmlFor="owner-setup-code">Setup code</Label>
                          <Input
                            id="owner-setup-code"
                            value={setupCode}
                            onChange={(event) =>
                              setSetupCode(event.target.value.toUpperCase())
                            }
                            autoComplete="one-time-code"
                            spellCheck={false}
                          />
                        </div>
                        <Button
                          onClick={() => void beginDeviceFlow()}
                          disabled={
                            startDevice.isPending || verifyCode.isPending
                          }
                        >
                          {startDevice.isPending || verifyCode.isPending ? (
                            <Loader2 className="size-4 animate-spin" />
                          ) : (
                            <Github className="size-4" />
                          )}
                          Connect GitHub identity
                        </Button>
                      </div>
                    </div>
                  )}
                </div>
              ) : null}
              <StageActions
                back={() => moveTo("instance")}
                next={() => moveTo("networking")}
                nextLabel="Continue to networking"
                nextDisabled={!state?.github.connected || !ownerConfirmed}
              />
            </>
          ) : null}

          {activeStep === "networking" ? (
            <>
              <StageHeader
                eyebrow="03 / Networking"
                title="Choose production ingress"
                description="Quick Tunnel is only for this setup session. Production uses the reviewed mode below."
              />
              <div className="grid gap-3 sm:grid-cols-2">
                <ChoiceCard
                  active={networkMode === "cloudflare_tunnel"}
                  icon={Cloud}
                  title="Named Cloudflare Tunnel"
                  description="Recommended. Creates a named tunnel and proxied DNS record for the final hostname."
                  onClick={() =>
                    configForm.setValue("network_mode", "cloudflare_tunnel", {
                      shouldDirty: true,
                    })
                  }
                />
                <ChoiceCard
                  active={networkMode === "public_ip"}
                  icon={Wifi}
                  title="Public IP"
                  description="Binds the bundled proxy publicly. Put TLS termination in front of it."
                  onClick={() =>
                    configForm.setValue("network_mode", "public_ip", {
                      shouldDirty: true,
                    })
                  }
                />
                <ChoiceCard
                  active={networkMode === "reverse_proxy"}
                  icon={Network}
                  title="Existing reverse proxy"
                  description="Keep proxy binding private and route the configured URL from your own ingress."
                  onClick={() =>
                    configForm.setValue("network_mode", "reverse_proxy", {
                      shouldDirty: true,
                    })
                  }
                />
                <ChoiceCard
                  active={networkMode === "local_only"}
                  icon={ServerCog}
                  title="Local only"
                  description="Keep the proxy on loopback for a private operator installation."
                  onClick={() =>
                    configForm.setValue("network_mode", "local_only", {
                      shouldDirty: true,
                    })
                  }
                />
              </div>
              {networkMode === "cloudflare_tunnel" ? (
                <div className="mt-6 space-y-5 rounded-xl border border-stealth-border p-5">
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <p className="text-sm font-medium text-white">
                        Cloudflare connection
                      </p>
                      <p className="mt-1 text-xs text-slate-500">
                        OAuth is preferred. The API token fallback is checked
                        before it is saved.
                      </p>
                    </div>
                    {cloudflareConnected ? (
                      <StatusPill status="ready">Connected</StatusPill>
                    ) : null}
                  </div>
                  {!cloudflareConnected ? (
                    <div className="grid gap-3 sm:grid-cols-2">
                      <Button
                        type="button"
                        onClick={() => void startCloudflareAuthorization()}
                        disabled={startCloudflareOAuth.isPending}
                      >
                        {startCloudflareOAuth.isPending ? (
                          <Loader2 className="size-4 animate-spin" />
                        ) : (
                          <Cloud className="size-4" />
                        )}
                        Connect with Cloudflare OAuth
                      </Button>
                      <div className="flex gap-2">
                        <Input
                          type="password"
                          placeholder="Scoped API token"
                          value={cloudflareToken}
                          onChange={(event) =>
                            setCloudflareToken(event.target.value)
                          }
                          autoComplete="new-password"
                        />
                        <Button
                          type="button"
                          variant="outline"
                          onClick={() => void connectCloudflareToken()}
                          disabled={saveCloudflareToken.isPending}
                        >
                          Use token
                        </Button>
                      </div>
                    </div>
                  ) : (
                    <>
                      <div className="grid gap-4 sm:grid-cols-2">
                        <Field
                          id="cloudflare-account"
                          label="Cloudflare account"
                        >
                          <select
                            id="cloudflare-account"
                            value={selectedAccount}
                            onChange={(event) => {
                              setCloudflareAccountID(event.target.value);
                              setCloudflareZoneID("");
                            }}
                            className="min-h-11 w-full rounded-lg border border-stealth-border bg-stealth-panel px-3 text-sm text-white focus:border-cyan-300/60"
                          >
                            <option value="">Select an account</option>
                            {(accounts.data?.accounts ?? []).map((account) => (
                              <option key={account.id} value={account.id}>
                                {account.name}
                              </option>
                            ))}
                          </select>
                        </Field>
                        <Field id="cloudflare-zone" label="Cloudflare zone">
                          <select
                            id="cloudflare-zone"
                            value={selectedZone}
                            onChange={(event) =>
                              setCloudflareZoneID(event.target.value)
                            }
                            disabled={!selectedAccount}
                            className="min-h-11 w-full rounded-lg border border-stealth-border bg-stealth-panel px-3 text-sm text-white focus:border-cyan-300/60 disabled:opacity-50"
                          >
                            <option value="">Select a zone</option>
                            {(zones.data?.zones ?? []).map((zone) => (
                              <option key={zone.id} value={zone.id}>
                                {zone.name}
                              </option>
                            ))}
                          </select>
                        </Field>
                      </div>
                      <Field
                        id="cloudflare-hostname"
                        label="Production hostname"
                        hint="The hostname must be inside the selected zone."
                      >
                        <Input
                          id="cloudflare-hostname"
                          placeholder="console.example.com"
                          {...configForm.register("hostname")}
                        />
                      </Field>
                      <div className="flex flex-wrap items-center gap-3">
                        <Button
                          type="button"
                          onClick={() => void createTunnel()}
                          disabled={createCloudflareTunnel.isPending}
                        >
                          {createCloudflareTunnel.isPending ? (
                            <Loader2 className="size-4 animate-spin" />
                          ) : (
                            <Cloud className="size-4" />
                          )}
                          {tunnelReady
                            ? "Verify named tunnel again"
                            : "Create named tunnel"}
                        </Button>
                        {tunnelReady ? (
                          <StatusPill status="ready">
                            Tunnel and DNS ready
                          </StatusPill>
                        ) : null}
                      </div>
                    </>
                  )}
                </div>
              ) : (
                <div className="mt-6 rounded-xl border border-stealth-border bg-black/10 p-5">
                  <p className="text-sm font-medium text-white">Public URL</p>
                  <p className="mt-1 text-xs leading-5 text-slate-500">
                    {watchedConfig.public_url}
                  </p>
                  <p className="mt-3 text-xs leading-5 text-amber-200/80">
                    You are responsible for TLS termination and firewall policy
                    for this mode.
                  </p>
                </div>
              )}
              <StageActions
                back={() => moveTo("github")}
                next={async () => {
                  try {
                    await persistConfig(configForm.getValues());
                    moveTo("data");
                  } catch (error) {
                    setActionError(error);
                  }
                }}
                nextLabel="Save and configure data"
                nextDisabled={
                  networkMode === "cloudflare_tunnel" && !tunnelReady
                }
                pending={saveConfig.isPending}
              />
            </>
          ) : null}

          {activeStep === "data" ? (
            <>
              <StageHeader
                eyebrow="04 / Database & Redis"
                title="Choose durable dependencies"
                description="Bundled services are the portable default. External endpoints are never started by the setup Compose project and must pass a live test."
              />
              <div className="space-y-7">
                <DependencySection
                  icon={Database}
                  title="PostgreSQL"
                  mode={databaseMode}
                  onModeChange={(value) =>
                    configForm.setValue("database_mode", value, {
                      shouldDirty: true,
                    })
                  }
                  tested={Boolean(state?.draft.database_tested)}
                  testing={testDatabase.isPending}
                  onTest={() => void testDatabaseConnection()}
                >
                  <Field
                    id="database-url"
                    label="PostgreSQL URL"
                    hint="The URL is kept server-side and is not returned in setup status."
                  >
                    <Input
                      id="database-url"
                      type="text"
                      placeholder="postgresql://user:password@db.example.com/stealth"
                      autoComplete="off"
                      {...configForm.register("database_url")}
                    />
                  </Field>
                </DependencySection>
                <DependencySection
                  icon={Wifi}
                  title="Redis"
                  mode={redisMode}
                  onModeChange={(value) =>
                    configForm.setValue("redis_mode", value, {
                      shouldDirty: true,
                    })
                  }
                  tested={Boolean(state?.draft.redis_tested)}
                  testing={testRedis.isPending}
                  onTest={() => void testRedisConnection()}
                >
                  <Field
                    id="redis-url"
                    label="Redis URL"
                    hint="Use rediss:// when the provider requires TLS."
                  >
                    <Input
                      id="redis-url"
                      type="text"
                      placeholder="rediss://:password@redis.example.com:6379/0"
                      autoComplete="off"
                      {...configForm.register("redis_url")}
                    />
                  </Field>
                </DependencySection>
              </div>
              <StageActions
                back={() => moveTo("networking")}
                next={async () => {
                  try {
                    await persistConfig(configForm.getValues());
                    moveTo("storage");
                  } catch (error) {
                    setActionError(error);
                  }
                }}
                nextLabel="Save and configure storage"
                nextDisabled={!dataReady}
                pending={saveConfig.isPending}
              />
            </>
          ) : null}

          {activeStep === "storage" ? (
            <>
              <StageHeader
                eyebrow="05 / Storage"
                title="Choose artifact storage"
                description="Local storage is persistent on this host. S3-compatible storage is tested before it is written into the final production environment."
              />
              <div className="grid gap-3 sm:grid-cols-2">
                <ChoiceCard
                  active={storageMode === "local"}
                  icon={HardDrive}
                  title="Local volume"
                  description="Use the persistent stealth_storage volume on this host."
                  onClick={() =>
                    configForm.setValue("storage_mode", "local", {
                      shouldDirty: true,
                    })
                  }
                />
                <ChoiceCard
                  active={storageMode === "s3"}
                  icon={Cloud}
                  title="S3-compatible"
                  description="Use an external object store for durable artifacts and files."
                  onClick={() =>
                    configForm.setValue("storage_mode", "s3", {
                      shouldDirty: true,
                    })
                  }
                />
              </div>
              {storageMode === "s3" ? (
                <div className="mt-6 space-y-5 rounded-xl border border-stealth-border p-5">
                  <div className="grid gap-5 sm:grid-cols-2">
                    <Field id="storage-endpoint" label="Endpoint">
                      <Input
                        id="storage-endpoint"
                        type="url"
                        placeholder="https://s3.example.com"
                        {...configForm.register("storage_s3_endpoint")}
                      />
                    </Field>
                    <Field id="storage-region" label="Region">
                      <Input
                        id="storage-region"
                        placeholder="us-east-1"
                        {...configForm.register("storage_s3_region")}
                      />
                    </Field>
                    <Field id="storage-bucket" label="Bucket">
                      <Input
                        id="storage-bucket"
                        placeholder="stealth-production"
                        {...configForm.register("storage_s3_bucket")}
                      />
                    </Field>
                    <Field
                      id="storage-prefix"
                      label="Prefix"
                      hint="Optional path prefix."
                    >
                      <Input
                        id="storage-prefix"
                        placeholder="stealth"
                        {...configForm.register("storage_s3_prefix")}
                      />
                    </Field>
                    <Field id="storage-access-key" label="Access key">
                      <Input
                        id="storage-access-key"
                        autoComplete="off"
                        {...configForm.register("storage_s3_access_key")}
                      />
                    </Field>
                    <Field id="storage-secret-key" label="Secret key">
                      <Input
                        id="storage-secret-key"
                        type="password"
                        autoComplete="new-password"
                        {...configForm.register("storage_s3_secret_key")}
                      />
                    </Field>
                  </div>
                  <div className="flex flex-wrap gap-5 text-sm text-slate-300">
                    <label className="flex min-h-11 items-center gap-2">
                      <input
                        type="checkbox"
                        className="size-4 accent-cyan-300"
                        {...configForm.register("storage_s3_use_ssl")}
                      />{" "}
                      Use TLS
                    </label>
                    <label className="flex min-h-11 items-center gap-2">
                      <input
                        type="checkbox"
                        className="size-4 accent-cyan-300"
                        {...configForm.register("storage_s3_path_style")}
                      />{" "}
                      Force path style
                    </label>
                  </div>
                  <div className="flex flex-wrap items-center gap-3 border-t border-stealth-border pt-5">
                    <Button
                      type="button"
                      onClick={() => void testStorageConnection()}
                      disabled={testStorage.isPending}
                    >
                      {testStorage.isPending ? (
                        <Loader2 className="size-4 animate-spin" />
                      ) : (
                        <Wifi className="size-4" />
                      )}
                      Test storage
                    </Button>
                    {state?.draft.storage_tested ? (
                      <StatusPill status="ready">Storage tested</StatusPill>
                    ) : (
                      <StatusPill status="warning">Test required</StatusPill>
                    )}
                  </div>
                </div>
              ) : (
                <div className="mt-6 rounded-xl border border-stealth-border bg-black/10 p-5 text-sm leading-6 text-slate-400">
                  The setup API will verify that the local storage volume is
                  writable during the system check.
                </div>
              )}
              <StageActions
                back={() => moveTo("data")}
                next={async () => {
                  try {
                    await persistConfig(configForm.getValues());
                    moveTo("review");
                  } catch (error) {
                    setActionError(error);
                  }
                }}
                nextLabel="Review installation"
                nextDisabled={!storageReady}
                pending={saveConfig.isPending}
              />
            </>
          ) : null}

          {activeStep === "review" ? (
            <>
              <StageHeader
                eyebrow="06 / Review"
                title="Review the production handoff"
                description="This is the final configuration the setup service will write before it starts the production Compose project."
              />
              <dl className="rounded-xl border border-stealth-border px-4">
                <ReviewRow
                  label="Instance"
                  value={
                    watchedConfig.instance_name ?? defaultConfig.instance_name
                  }
                />
                <ReviewRow
                  label="Public URL"
                  value={watchedConfig.public_url ?? defaultConfig.public_url}
                />
                <ReviewRow
                  label="Networking"
                  value={networkMode.replaceAll("_", " ")}
                />
                <ReviewRow
                  label="GitHub"
                  value={
                    state?.github.client_id
                      ? `Connected (${state.github.client_id})`
                      : "Not connected"
                  }
                />
                <ReviewRow
                  label="PostgreSQL"
                  value={
                    databaseMode === "bundled"
                      ? "Bundled"
                      : "External URL tested"
                  }
                />
                <ReviewRow
                  label="Redis"
                  value={
                    redisMode === "bundled" ? "Bundled" : "External URL tested"
                  }
                />
                <ReviewRow
                  label="Storage"
                  value={
                    storageMode === "local"
                      ? "Local volume"
                      : "S3-compatible settings tested"
                  }
                />
                {networkMode === "cloudflare_tunnel" ? (
                  <ReviewRow
                    label="Tunnel"
                    value={watchedConfig.hostname || "Not configured"}
                  />
                ) : null}
              </dl>
              <div className="mt-6 rounded-xl border border-amber-300/20 bg-amber-300/[0.06] p-4 text-xs leading-5 text-amber-100/80">
                Installation starts production services and may create
                provider-side resources. The operation is resumable, but
                provider side effects are not rolled back automatically.
              </div>
              <StageActions
                back={() => moveTo("storage")}
                next={() => void startProductionInstall()}
                nextLabel="Install Stealth"
                nextDisabled={!ownerConfirmed || !dataReady || !storageReady}
                pending={isPending}
              />
            </>
          ) : null}

          {activeStep === "install" ? (
            <>
              <StageHeader
                eyebrow="07 / Install"
                title={
                  installViewState?.phase === "failed"
                    ? "Installation needs attention"
                    : "Installing Stealth"
                }
                description={
                  installViewState?.phase === "failed"
                    ? "The production stack is still repairable. Review the safe error below and retry after correcting the underlying issue."
                    : "The setup service is running the shared install engine. Keep this window open until the production session handoff completes."
                }
              />
              <div className="rounded-xl border border-stealth-border p-5">
                <div className="flex items-center justify-between gap-4">
                  <div>
                    <p className="text-sm font-medium text-white">
                      {installViewState?.step ?? "Preparing installation"}
                    </p>
                    <p className="mt-1 text-xs text-slate-500">
                      {sseConnected
                        ? "Live progress connected"
                        : "Reconnecting to live progress"}
                    </p>
                  </div>
                  {installViewState?.phase === "failed" ? (
                    <StatusPill status="error">Failed</StatusPill>
                  ) : (
                    <StatusPill status="pending">Working</StatusPill>
                  )}
                </div>
                <div className="mt-5 space-y-2">
                  {[
                    "Configuration and secrets",
                    "Release images",
                    "PostgreSQL and Redis",
                    "Database migrations",
                    "API, Worker, Console, and Proxy",
                    "Health and readiness verification",
                  ].map((label) => {
                    const done =
                      installViewState?.phase !== "failed" &&
                      installViewState?.step === label;
                    return (
                      <div
                        key={label}
                        className={`flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm ${done ? "bg-cyan-300/[0.06] text-cyan-100" : "text-slate-500"}`}
                      >
                        <span
                          className={`flex size-5 items-center justify-center rounded-full border ${done ? "border-cyan-300/50 text-cyan-300" : "border-stealth-border"}`}
                        >
                          {done ? (
                            <Loader2 className="size-3 animate-spin" />
                          ) : null}
                        </span>
                        {label}
                      </div>
                    );
                  })}
                </div>
                {installViewState?.phase === "failed" &&
                installViewState.error_message ? (
                  <p
                    className="mt-5 rounded-lg border border-rose-300/20 bg-rose-400/[0.08] px-3 py-2.5 text-xs leading-5 text-rose-200"
                    role="alert"
                  >
                    {installViewState.error_message}
                  </p>
                ) : null}
              </div>
              {installViewState?.phase === "failed" ? (
                <Button
                  className="mt-6"
                  onClick={() => void retryProductionInstall()}
                  disabled={isPending}
                >
                  <RefreshCw className="size-4" /> Retry installation
                </Button>
              ) : null}
              <p className="mt-6 text-xs leading-5 text-slate-600">
                If the browser disconnects, return to this URL while the
                temporary setup service is available. The latest state is
                persisted server-side.
              </p>
            </>
          ) : null}
        </section>
      </div>
    </SetupShell>
  );
}

function SetupShell({
  children,
  currentStep,
}: {
  children: React.ReactNode;
  currentStep: SetupStep;
}) {
  return (
    <main className="min-h-screen bg-stealth-bg px-4 py-5 text-white sm:px-6 sm:py-8">
      <div className="mx-auto max-w-6xl">
        <header className="mb-8 flex items-center justify-between gap-4">
          <BrandMark />
          <div className="text-right">
            <p className="text-xs font-medium uppercase tracking-[0.18em] text-slate-600">
              First-run setup
            </p>
            <p className="mt-1 text-xs text-slate-500">
              {setupSteps.find((item) => item.id === currentStep)?.label ??
                "Setup"}
            </p>
          </div>
        </header>
        {children}
        <footer className="mt-8 flex flex-col gap-2 border-t border-stealth-border pt-4 text-xs text-slate-600 sm:flex-row sm:items-center sm:justify-between">
          <span>Stealth Console · setup state stays on the server</span>
          <Link href="/login" className="text-cyan-300 hover:text-cyan-200">
            Already have access? Sign in
          </Link>
        </footer>
      </div>
    </main>
  );
}

function LoadingPanel({ label }: { label: string }) {
  return (
    <Card className="mx-auto max-w-xl border-stealth-border bg-stealth-panel">
      <CardContent
        className="flex items-center gap-3 p-6 text-sm text-slate-400"
        aria-live="polite"
      >
        <Loader2 className="size-5 animate-spin text-cyan-300" /> {label}
      </CardContent>
    </Card>
  );
}

function InfoCard({
  icon: Icon,
  title,
  description,
}: {
  icon: typeof Terminal;
  title: string;
  description: string;
}) {
  return (
    <div className="rounded-xl border border-stealth-border bg-stealth-panel p-4">
      <Icon className="size-5 text-cyan-300" />
      <p className="mt-4 text-sm font-medium text-white">{title}</p>
      <p className="mt-1 text-xs leading-5 text-slate-600">{description}</p>
    </div>
  );
}

function DependencySection({
  icon: Icon,
  title,
  mode,
  onModeChange,
  tested,
  testing,
  onTest,
  children,
}: {
  icon: typeof Database;
  title: string;
  mode: "bundled" | "external";
  onModeChange: (value: "bundled" | "external") => void;
  tested: boolean;
  testing: boolean;
  onTest: () => void;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-xl border border-stealth-border p-5">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex items-start gap-3">
          <Icon className="mt-0.5 size-5 shrink-0 text-cyan-300" />
          <div>
            <h2 className="text-sm font-medium text-white">{title}</h2>
            <p className="mt-1 text-xs text-slate-600">
              {mode === "bundled"
                ? "Managed by this setup Compose project."
                : "Your endpoint is tested before install."}
            </p>
          </div>
        </div>
        <div className="flex gap-1 rounded-lg border border-stealth-border p-1">
          {(["bundled", "external"] as const).map((option) => (
            <button
              key={option}
              type="button"
              onClick={() => onModeChange(option)}
              className={`min-h-9 rounded-md px-3 text-xs font-medium ${mode === option ? "bg-cyan-300 text-slate-950" : "text-slate-500 hover:text-white"}`}
            >
              {option === "bundled" ? "Bundled" : "External"}
            </button>
          ))}
        </div>
      </div>
      {mode === "external" ? (
        <div className="mt-5 space-y-4">
          {children}
          <div className="flex flex-wrap items-center gap-3">
            <Button
              type="button"
              variant="outline"
              onClick={onTest}
              disabled={testing}
            >
              {testing ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                <Wifi className="size-4" />
              )}{" "}
              Test {title}
            </Button>
            {tested ? (
              <StatusPill status="ready">Connection tested</StatusPill>
            ) : (
              <StatusPill status="warning">Test required</StatusPill>
            )}
          </div>
        </div>
      ) : null}
    </section>
  );
}

function DeviceFlow({
  device,
  copyState,
  onCopy,
  pollError,
  onCancel,
}: {
  device: components["schemas"]["GitHubDeviceResponse"];
  copyState: "idle" | "copied" | "failed";
  onCopy: () => void;
  pollError: unknown;
  onCancel: () => void;
}) {
  return (
    <div
      className="space-y-5 rounded-xl border border-stealth-border p-5"
      aria-live="polite"
    >
      <div className="text-center">
        <p className="text-sm text-slate-400">Enter this code on GitHub</p>
        <p className="mt-3 font-mono text-2xl font-semibold tracking-[0.2em] text-violet-100">
          {device.user_code}
        </p>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="mt-4"
          onClick={onCopy}
        >
          {copyState === "copied" ? (
            <Check className="size-4" />
          ) : (
            <Copy className="size-4" />
          )}
          {copyState === "copied" ? "Copied" : "Copy code"}
        </Button>
        {copyState === "failed" ? (
          <p className="mt-2 text-xs text-amber-200">
            Copy was unavailable. Select the code manually.
          </p>
        ) : null}
      </div>
      <Button asChild className="w-full">
        <a
          href={device.verification_uri}
          target="_blank"
          rel="noopener noreferrer"
        >
          <ExternalLink className="size-4" /> Open GitHub
        </a>
      </Button>
      <p className="text-center text-sm text-slate-400" role="status">
        <Loader2 className="mr-1 inline size-4 animate-spin text-cyan-300" />{" "}
        Waiting for GitHub authorization
      </p>
      {pollError ? (
        <p
          className="rounded-lg border border-amber-300/20 bg-amber-300/[0.08] px-3 py-2.5 text-xs leading-5 text-amber-100"
          role="alert"
        >
          {safeError(pollError)} Retrying safely.
        </p>
      ) : null}
      <Button
        type="button"
        variant="ghost"
        className="w-full"
        onClick={onCancel}
      >
        Cancel GitHub authorization
      </Button>
    </div>
  );
}
