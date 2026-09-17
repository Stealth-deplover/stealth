"use client";

import Link from "next/link";
import { useEffect, useRef } from "react";
import {
  ArrowLeft,
  ArrowRight,
  Check,
  CheckCircle2,
  CircleAlert,
  Cloud,
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
import { ApiError } from "@/api/client";
import { errorMessage } from "@/components/feedback/error-state";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { useBrowserSetupFlow } from "./browser-setup-flow";
import {
  defaultConfig,
  setupSteps,
  type SetupStep,
} from "./browser-setup-model";

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
    if (error.code === "cloudflare_oauth_inactive") {
      return "Cloudflare OAuth is experimental and inactive. Use a scoped API token instead.";
    }
    if (error.code === "cloudflare_token_rejected") {
      return "Cloudflare rejected the token. Check Account Settings Read, Cloudflare Tunnel Edit, Zone Read, and DNS Edit on the selected resources.";
    }
  }
  return errorMessage(error);
}

export function submitGitHubManifest(actionURL: string, manifest: string) {
  const target = new URL(actionURL);
  if (
    target.origin !== "https://github.com" ||
    target.pathname !== "/settings/apps/new"
  ) {
    throw new Error("GitHub returned an invalid App Manifest destination.");
  }
  const form = document.createElement("form");
  form.method = "post";
  form.action = target.toString();
  form.hidden = true;
  const field = document.createElement("input");
  field.type = "hidden";
  field.name = "manifest";
  field.value = manifest;
  form.appendChild(field);
  document.body.appendChild(form);
  form.submit();
}

function BrandMark() {
  return (
    <div className="flex items-center gap-2.5">
      <span className="flex size-9 items-center justify-center rounded-md border border-acid-lime/40 bg-acid-lime text-sm font-semibold text-void">
        S
      </span>
      <span className="text-sm font-semibold tracking-[-0.012em] text-paper">
        Stealth
      </span>
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
      className={`flex min-h-28 w-full items-start gap-3 rounded-lg border p-4 text-left transition-colors duration-150 focus-visible:ring-2 focus-visible:ring-acid-lime/40 ${active ? "border-acid-lime/60 bg-acid-lime/[0.08]" : "border-graphite bg-carbon hover:border-smoke"}`}
    >
      <Icon
        className={`mt-0.5 size-5 shrink-0 ${active ? "text-acid-lime" : "text-fog"}`}
      />
      <span>
        <span className="block text-sm font-medium text-paper">{title}</span>
        <span className="mt-1 block text-xs leading-5 text-fog">
          {description}
        </span>
      </span>
      {active ? (
        <Check className="ml-auto size-4 shrink-0 text-acid-lime" />
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
    pending: "border-acid-lime/25 bg-acid-lime/10 text-mist",
    warning: "border-amber-300/25 bg-amber-300/10 text-amber-200",
    error: "border-rose-300/25 bg-rose-300/10 text-rose-200",
  }[status];
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-xs border px-1.5 py-1 text-xs font-medium ${styles}`}
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
      <p className="mb-2 text-xs font-medium uppercase tracking-[0.14em] text-fog">
        {eyebrow}
      </p>
      <h1 className="text-2xl font-semibold leading-tight tracking-[-0.022em] text-paper sm:text-3xl">
        {title}
      </h1>
      <p className="mt-3 max-w-2xl text-sm leading-6 text-fog">{description}</p>
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

function handoffURL(publicURL: string) {
  try {
    const target = new URL(publicURL);
    if (
      (target.protocol !== "http:" && target.protocol !== "https:") ||
      target.username ||
      target.password ||
      target.search ||
      target.hash ||
      (target.pathname !== "" && target.pathname !== "/")
    ) {
      return "";
    }
    target.pathname = "/v1/setup/handoff";
    return target.toString();
  } catch {
    return "";
  }
}

function HandoffSubmission({
  publicURL,
  token,
}: {
  publicURL: string;
  token: string;
}) {
  const formRef = useRef<HTMLFormElement>(null);
  const action = handoffURL(publicURL);

  useEffect(() => {
    if (action) formRef.current?.requestSubmit();
  }, [action, token]);

  if (!action) return null;
  return (
    <form
      ref={formRef}
      method="post"
      action={action}
      className="hidden"
      aria-hidden="true"
    >
      <input type="hidden" name="token" value={token} />
    </form>
  );
}

export function BrowserSetupView() {
  const {
    accounts,
    actionError,
    activeStep,
    authorizeGitHubOwner,
    callbackError,
    callbackNotice,
    checks,
    checksPass,
    cloudflareConnected,
    cloudflareToken,
    configForm,
    createCloudflareTunnel,
    continueCloudflareSetup,
    dataReady,
    databaseMode,
    handoffReady,
    handoffToken,
    installPhase,
    installPublicURL,
    installViewState,
    isPending,
    manualForm,
    moveTo,
    networkMode,
    notice,
    ownerConfirmed,
    persistConfig,
    preflight,
    providerMode,
    retryProductionInstall,
    saveCloudflareToken,
    saveConfig,
    saveManual,
    saveManualGitHub,
    selectedAccount,
    selectedZone,
    connectCloudflareToken,
    setupCode,
    setupStatus,
    setupVerified,
    sseConnected,
    setActionError,
    setCloudflareAccountID,
    setCloudflareToken,
    setCloudflareZoneID,
    setProviderMode,
    setSetupCode,
    startAuthorization,
    startManifest,
    startProductionInstall,
    state,
    redisMode,
    storageMode,
    storageReady,
    testDatabase,
    testDatabaseConnection,
    testRedis,
    testRedisConnection,
    testStorage,
    testStorageConnection,
    tunnelReady,
    verifyCode,
    verifySetupCode,
    watchedConfig,
    zones,
  } = useBrowserSetupFlow();

  const currentIndex = setupSteps.findIndex((item) => item.id === activeStep);
  const canOpenStep = (target: SetupStep) => {
    const targetIndex = setupSteps.findIndex((item) => item.id === target);
    return targetIndex <= currentIndex && activeStep !== "install";
  };

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

  if (installPhase === "complete") {
    return (
      <SetupShell currentStep="install">
        {handoffReady ? (
          <HandoffSubmission
            publicURL={installPublicURL}
            token={handoffToken}
          />
        ) : null}
        <ProductionLink publicURL={installPublicURL} />
      </SetupShell>
    );
  }

  return (
    <SetupShell currentStep={activeStep}>
      {handoffReady ? (
        <HandoffSubmission publicURL={installPublicURL} token={handoffToken} />
      ) : null}
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
                    className={`flex min-h-11 items-center gap-2 rounded-lg px-2.5 py-2 text-left text-xs transition-colors duration-150 focus-visible:ring-2 focus-visible:ring-acid-lime/40 lg:w-full ${active ? "bg-acid-lime/[0.08] text-mist" : available ? "text-fog hover:bg-white/[0.04] hover:text-paper" : "cursor-not-allowed text-slate-700"}`}
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
                            GitHub opens its registration page, then returns the
                            App credentials and opens a second browser
                            authorization step for the first owner.
                          </p>
                        </div>
                      </div>
                      <Button
                        className="mt-5"
                        onClick={() =>
                          void startManifest
                            .mutateAsync()
                            .then((result) => {
                              if (!result?.manifest_url || !result.manifest) {
                                throw new Error(
                                  "GitHub App Manifest data was not returned.",
                                );
                              }
                              submitGitHubManifest(
                                result.manifest_url,
                                result.manifest,
                              );
                            })
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
                          : "Create App and authorize owner"}
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
                  ) : (
                    <div className="rounded-xl border border-stealth-border p-5">
                      <p className="text-sm font-medium text-white">
                        Authorize the first owner in GitHub
                      </p>
                      <p className="mt-1 text-xs leading-5 text-slate-500">
                        Stealth will open GitHub&apos;s browser authorization
                        page. After approval, the callback creates the first
                        Instance Owner. No password or device code is required.
                      </p>
                      {state.github.mode === "manual" ? (
                        <p className="mt-3 rounded-lg border border-amber-300/20 bg-amber-300/[0.06] px-3 py-2.5 text-xs leading-5 text-amber-100">
                          Manual Apps must have the exact HTTPS callback URL
                          <code className="mx-1 break-all text-amber-200">
                            /v1/setup/github/authorize/callback
                          </code>
                          registered in GitHub App settings.
                        </p>
                      ) : null}
                      <Button
                        className="mt-5"
                        onClick={() => void authorizeGitHubOwner()}
                        disabled={startAuthorization.isPending}
                      >
                        {startAuthorization.isPending ? (
                          <Loader2 className="size-4 animate-spin" />
                        ) : (
                          <Github className="size-4" />
                        )}
                        {startAuthorization.isPending
                          ? "Opening GitHub"
                          : "Authorize owner in GitHub"}
                      </Button>
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
                        Verify a scoped API token here. Stealth discovers the
                        account and domain, then configures the named tunnel and
                        DNS for you.
                      </p>
                    </div>
                    {cloudflareConnected ? (
                      <StatusPill status="ready">
                        <CheckCircle2 className="size-3.5" />
                        Cloudflare connected
                      </StatusPill>
                    ) : null}
                  </div>
                  {!cloudflareConnected ? (
                    <div className="space-y-3">
                      <Field
                        id="cloudflare-api-token"
                        label="API Token"
                        hint="The token is sent only to the setup API and is never returned to browser state."
                      >
                        <div className="flex flex-col gap-2 sm:flex-row">
                          <Input
                            id="cloudflare-api-token"
                            type="password"
                            placeholder="Scoped Cloudflare API token"
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
                            className="sm:min-w-32"
                          >
                            {saveCloudflareToken.isPending ? (
                              <Loader2 className="size-4 animate-spin" />
                            ) : null}
                            Verify Token
                          </Button>
                        </div>
                      </Field>
                      <p className="text-xs leading-5 text-slate-500">
                        Create a custom token with Account: Cloudflare Tunnel
                        Edit and Account Settings Read; Zone: Zone Read and DNS
                        Edit. Scope it to the account and domain you will use.
                        Global API keys are not accepted.
                      </p>
                    </div>
                  ) : (
                    <>
                      <div className="grid gap-4 sm:grid-cols-2">
                        <Field id="cloudflare-account" label="Account">
                          <select
                            id="cloudflare-account"
                            value={selectedAccount}
                            onChange={(event) => {
                              setCloudflareAccountID(event.target.value);
                              setCloudflareZoneID("");
                            }}
                            className="min-h-11 w-full rounded-lg border border-stealth-border bg-stealth-panel px-3 text-sm text-white focus:border-cyan-300/60"
                          >
                            <option value="">
                              {accounts.isPending
                                ? "Discovering accounts…"
                                : "Select an account"}
                            </option>
                            {(accounts.data?.accounts ?? []).map((account) => (
                              <option key={account.id} value={account.id}>
                                {account.name}
                              </option>
                            ))}
                          </select>
                          {accounts.isError ? (
                            <p className="text-xs leading-5 text-rose-300">
                              Account discovery failed. Check the token scope
                              and try again.
                            </p>
                          ) : null}
                        </Field>
                        <Field id="cloudflare-zone" label="Domain">
                          <select
                            id="cloudflare-zone"
                            value={selectedZone}
                            onChange={(event) =>
                              setCloudflareZoneID(event.target.value)
                            }
                            disabled={!selectedAccount}
                            className="min-h-11 w-full rounded-lg border border-stealth-border bg-stealth-panel px-3 text-sm text-white focus:border-cyan-300/60 disabled:opacity-50"
                          >
                            <option value="">
                              {zones.isPending
                                ? "Discovering domains…"
                                : "Select a domain"}
                            </option>
                            {(zones.data?.zones ?? []).map((zone) => (
                              <option key={zone.id} value={zone.id}>
                                {zone.name}
                              </option>
                            ))}
                          </select>
                          {zones.isError ? (
                            <p className="text-xs leading-5 text-rose-300">
                              Domain discovery failed. Check Zone Read access
                              for the selected account.
                            </p>
                          ) : null}
                        </Field>
                      </div>
                      <Field
                        id="cloudflare-hostname"
                        label="Dashboard hostname"
                        hint="The hostname must be inside the selected zone."
                      >
                        <Input
                          id="cloudflare-hostname"
                          placeholder="console.example.com"
                          {...configForm.register("hostname")}
                        />
                      </Field>
                      {tunnelReady ? (
                        <StatusPill status="ready">
                          <CheckCircle2 className="size-3.5" />
                          Tunnel and DNS ready
                        </StatusPill>
                      ) : (
                        <p className="text-xs leading-5 text-slate-500">
                          Continue provisions the named tunnel, ingress, DNS,
                          and cloudflared configuration automatically.
                        </p>
                      )}
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
                next={() => void continueCloudflareSetup()}
                nextLabel="Continue"
                nextDisabled={
                  networkMode === "cloudflare_tunnel" &&
                  (!cloudflareConnected ||
                    !selectedAccount ||
                    !selectedZone ||
                    !accounts.data?.accounts?.length ||
                    !zones.data?.zones?.length ||
                    accounts.isError ||
                    zones.isError ||
                    !watchedConfig.hostname?.trim() ||
                    createCloudflareTunnel.isPending)
                }
                pending={
                  saveConfig.isPending || createCloudflareTunnel.isPending
                }
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
                description="This is the final configuration the setup service will validate and persist before the host installer starts the production Compose project."
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
                    : installViewState?.phase === "install_requested"
                      ? "Your configuration was received. The host installer is preparing the installation."
                      : "The host installer is running the shared install engine. Keep this window open until the production session handoff completes."
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
    <main className="min-h-screen bg-void px-4 py-5 text-paper sm:px-6 sm:py-8">
      <div className="mx-auto max-w-[1200px]">
        <header className="mb-8 flex items-center justify-between gap-4">
          <BrandMark />
          <div className="text-right">
            <p className="text-xs font-medium uppercase tracking-[0.12em] text-fog">
              First-run setup
            </p>
            <p className="mt-1 text-xs text-fog">
              {setupSteps.find((item) => item.id === currentStep)?.label ??
                "Setup"}
            </p>
          </div>
        </header>
        {children}
        <footer className="mt-8 flex flex-col gap-2 border-t border-graphite pt-4 text-xs text-fog sm:flex-row sm:items-center sm:justify-between">
          <span>Stealth Console · setup state stays on the server</span>
          <Link
            href="/login"
            className="text-mist transition-colors duration-150 hover:text-acid-lime"
          >
            Already have access? Sign in
          </Link>
        </footer>
      </div>
    </main>
  );
}

function LoadingPanel({ label }: { label: string }) {
  return (
    <Card className="mx-auto max-w-xl bg-carbon">
      <CardContent
        className="flex items-center gap-3 p-6 text-sm text-fog"
        aria-live="polite"
      >
        <Loader2 className="size-5 animate-spin text-acid-lime" /> {label}
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
    <div className="rounded-lg border border-graphite bg-carbon p-4">
      <Icon className="size-5 text-fog" />
      <p className="mt-4 text-sm font-medium text-paper">{title}</p>
      <p className="mt-1 text-xs leading-5 text-fog">{description}</p>
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
    <section className="rounded-lg border border-graphite bg-carbon/40 p-5">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex items-start gap-3">
          <Icon className="mt-0.5 size-5 shrink-0 text-fog" />
          <div>
            <h2 className="text-sm font-medium text-paper">{title}</h2>
            <p className="mt-1 text-xs text-fog">
              {mode === "bundled"
                ? "Managed by this setup Compose project."
                : "Your endpoint is tested before install."}
            </p>
          </div>
        </div>
        <div className="flex gap-1 rounded-md border border-graphite p-1">
          {(["bundled", "external"] as const).map((option) => (
            <button
              key={option}
              type="button"
              onClick={() => onModeChange(option)}
              className={`min-h-11 rounded-sm px-3 text-xs font-medium transition-colors duration-150 ${mode === option ? "bg-acid-lime text-void" : "text-fog hover:text-paper"}`}
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
