"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import {
  Check,
  CheckCircle2,
  Copy,
  ExternalLink,
  Github,
  Loader2,
  ShieldCheck,
  XCircle,
} from "lucide-react";
import { ApiError } from "@/api/client";
import {
  usePollGitHubDeviceFlow,
  useStartGitHubDeviceFlow,
  useVerifyBootstrapCode,
} from "@/api/mutations";
import { useBootstrapStatus } from "@/api/queries";
import { errorMessage, ErrorState } from "@/components/feedback/error-state";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { AuthCard } from "@/features/auth/auth-flow-views";

const setupCodePattern =
  /^STEALTH-[A-HJ-NP-Z2-9]{4}-[A-HJ-NP-Z2-9]{4}-[A-HJ-NP-Z2-9]{4}$/i;

type SetupPhase = "code" | "verified" | "device" | "complete";
type GitHubDevice = {
  authorization_session_id: string;
  user_code: string;
  verification_uri: "https://github.com/login/device";
  expires_at: string;
  interval_seconds: number;
};

function setupErrorMessage(error: unknown) {
  if (error instanceof ApiError) {
    if (error.code === "invalid_bootstrap_code") {
      return "That setup code is invalid, expired, or already used.";
    }
    if (error.code === "bootstrap_complete") {
      return "Instance setup has already been completed. Sign in to continue.";
    }
    if (error.code === "github_authorization_expired") {
      return "GitHub authorization expired. Start a new authorization session.";
    }
    if (error.code === "github_access_denied") {
      return "GitHub authorization was cancelled. You can try again.";
    }
    if (error.code === "github_not_configured") {
      return "GitHub first-owner authentication is not configured on this installation.";
    }
  }
  return errorMessage(error);
}

function formatRemaining(expiresAt: string, now: number) {
  const remaining = Math.max(0, new Date(expiresAt).getTime() - now);
  const minutes = Math.floor(remaining / 60_000);
  const seconds = Math.floor((remaining % 60_000) / 1_000);
  return `${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
}

function CompletedView() {
  return (
    <AuthCard
      title="Instance setup is complete"
      description="The first Instance Owner has already been created for this Stealth installation."
    >
      <CheckCircle2 className="size-6 text-emerald-300" />
      <p className="mt-4 text-sm leading-6 text-slate-400">
        The one-time setup flow is permanently sealed. This page does not expose
        onboarding credentials or account details.
      </p>
      <Button asChild className="mt-6 w-full">
        <Link href="/login">Sign in to Stealth</Link>
      </Button>
    </AuthCard>
  );
}

export function SetupView() {
  const router = useRouter();
  const status = useBootstrapStatus();
  const verify = useVerifyBootstrapCode();
  const startDevice = useStartGitHubDeviceFlow();
  const pollDevice = usePollGitHubDeviceFlow();
  const [phase, setPhase] = useState<SetupPhase>("code");
  const [setupCode, setSetupCode] = useState("");
  const [codeError, setCodeError] = useState("");
  const [authorizationSessionID, setAuthorizationSessionID] = useState("");
  const [device, setDevice] = useState<GitHubDevice>();
  const [pollDelay, setPollDelay] = useState(5_000);
  const [pollError, setPollError] = useState<unknown>();
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">(
    "idle",
  );
  const [now, setNow] = useState(() => Date.now());
  const pollRef = useRef(pollDevice.mutateAsync);

  useEffect(() => {
    pollRef.current = pollDevice.mutateAsync;
  }, [pollDevice.mutateAsync]);

  useEffect(() => {
    if (phase !== "device" || !device) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1_000);
    return () => window.clearInterval(timer);
  }, [device, phase]);

  useEffect(() => {
    if (phase !== "device" || !device || !authorizationSessionID) return;
    let cancelled = false;
    const timer = window.setTimeout(async () => {
      if (new Date(device.expires_at).getTime() <= Date.now()) {
        setPollError(
          new ApiError(
            "authorization expired",
            410,
            "github_authorization_expired",
          ),
        );
        setPhase("verified");
        setDevice(undefined);
        return;
      }
      try {
        const result = await pollRef.current({
          authorization_session_id: authorizationSessionID,
        });
        if (cancelled || !result) return;
        if (result.status === "complete") {
          setPhase("complete");
          router.replace("/organizations");
          return;
        }
        setPollError(undefined);
        setPollDelay(Math.max(1, result.retry_after_seconds ?? 5) * 1_000);
      } catch (error) {
        if (cancelled) return;
        setPollError(error);
        if (
          error instanceof ApiError &&
          (error.code === "github_authorization_expired" ||
            error.code === "github_access_denied")
        ) {
          setDevice(undefined);
          setPhase("verified");
          return;
        }
        setPollDelay(5_000);
      }
    }, pollDelay);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [authorizationSessionID, device, phase, pollDelay, router]);

  if (status.isPending) {
    return (
      <Card className="w-full max-w-md border-stealth-border/80 bg-stealth-panel">
        <CardHeader className="p-7 pb-4">
          <CardTitle className="text-2xl">Checking instance setup</CardTitle>
          <CardDescription>
            Stealth is checking whether first-run onboarding is still available.
          </CardDescription>
        </CardHeader>
        <CardContent className="p-7 pt-2" aria-live="polite">
          <Loader2
            className="size-6 animate-spin text-cyan-300"
            aria-label="Loading"
          />
        </CardContent>
      </Card>
    );
  }

  if (status.error) {
    return (
      <ErrorState
        error={status.error}
        retry={() => void status.refetch()}
        title="Could not check instance setup"
      />
    );
  }

  if (!status.data?.setup_required) return <CompletedView />;

  const verifyCode = async () => {
    const normalized = setupCode.trim().toUpperCase();
    if (!setupCodePattern.test(normalized)) {
      setCodeError("Enter the setup code shown by the Stealth CLI.");
      return;
    }
    setCodeError("");
    setPollError(undefined);
    try {
      const result = await verify.mutateAsync({ setup_code: normalized });
      if (!result) return;
      setSetupCode(normalized);
      setAuthorizationSessionID(result.authorization_session_id);
      setPhase("verified");
    } catch {
      // The mutation retains the generic server error for the accessible alert.
    }
  };

  const beginGitHub = async () => {
    if (!authorizationSessionID || !setupCode) return;
    setPollError(undefined);
    try {
      const result = await startDevice.mutateAsync({
        authorization_session_id: authorizationSessionID,
        setup_code: setupCode,
      });
      if (!result) return;
      setDevice(result as GitHubDevice);
      setPollDelay(Math.max(1, result.interval_seconds) * 1_000);
      setNow(Date.now());
      setPhase("device");
    } catch {
      // The mutation retains the generic server error for the accessible alert.
    }
  };

  const resetToCode = () => {
    verify.reset();
    startDevice.reset();
    setPhase("code");
    setAuthorizationSessionID("");
    setDevice(undefined);
    setPollError(undefined);
    setCopyState("idle");
  };

  if (phase === "complete") {
    return (
      <AuthCard
        title="Instance Owner created"
        description="GitHub verified your identity and Stealth created a normal authenticated session."
      >
        <CheckCircle2 className="size-6 text-emerald-300" />
        <p className="mt-4 text-sm leading-6 text-slate-400" aria-live="polite">
          Bootstrap is permanently sealed. Opening the Console…
        </p>
      </AuthCard>
    );
  }

  return (
    <Card className="w-full max-w-md border-stealth-border/80 bg-stealth-panel">
      <CardHeader className="p-7 pb-4">
        <p className="mb-2 text-xs font-medium uppercase tracking-[0.18em] text-cyan-300">
          First-run security
        </p>
        <CardTitle className="text-2xl">
          {phase === "device"
            ? "Connect GitHub"
            : phase === "verified"
              ? "Create Instance Owner"
              : "Set up Stealth"}
        </CardTitle>
        <CardDescription>
          {phase === "device"
            ? "Authorize the GitHub identity that will become the first Instance Owner."
            : phase === "verified"
              ? "Use GitHub to verify your identity. No local password is created during first-run setup."
              : "Verify that you are operating the Stealth installation before continuing."}
        </CardDescription>
      </CardHeader>
      <CardContent className="p-7 pt-2">
        {phase === "code" ? (
          <form
            onSubmit={(event) => {
              event.preventDefault();
              void verifyCode();
            }}
            className="space-y-4"
            noValidate
          >
            <div className="rounded-xl border border-cyan-300/20 bg-cyan-300/[0.06] p-4 text-sm leading-6 text-slate-300">
              <ShieldCheck className="mb-2 size-5 text-cyan-300" />
              The setup code comes from the local CLI. It is sent only to the Go
              API and is never placed in the URL or browser storage.
            </div>
            <div className="space-y-2">
              <Label htmlFor="setup-code">Setup code</Label>
              <Input
                id="setup-code"
                type="text"
                inputMode="text"
                autoComplete="one-time-code"
                spellCheck={false}
                translate="no"
                value={setupCode}
                onChange={(event) =>
                  setSetupCode(event.target.value.toUpperCase())
                }
                aria-describedby={codeError ? "setup-code-error" : undefined}
              />
              {codeError ? (
                <p
                  id="setup-code-error"
                  className="text-xs text-rose-300"
                  role="alert"
                >
                  {codeError}
                </p>
              ) : null}
            </div>
            {verify.error ? (
              <p
                className="rounded-lg border border-rose-300/20 bg-rose-400/10 px-3 py-2 text-xs leading-5 text-rose-200"
                role="alert"
                aria-live="polite"
              >
                <XCircle className="mr-1 inline size-3.5" />
                {setupErrorMessage(verify.error)}
              </p>
            ) : null}
            <Button className="mt-2 h-10 w-full" disabled={verify.isPending}>
              {verify.isPending ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                <ShieldCheck className="size-4" />
              )}
              {verify.isPending ? "Verifying…" : "Verify setup code"}
            </Button>
          </form>
        ) : null}

        {phase === "verified" ? (
          <div aria-live="polite">
            <div className="rounded-xl border border-emerald-300/20 bg-emerald-300/[0.06] p-4 text-sm leading-6 text-slate-300">
              <CheckCircle2 className="mb-2 size-5 text-emerald-300" />
              Installation verified. GitHub will provide the identity for the
              first Instance Owner.
            </div>
            {startDevice.error || pollError ? (
              <p
                className="mt-4 rounded-lg border border-rose-300/20 bg-rose-400/10 px-3 py-2 text-xs leading-5 text-rose-200"
                role="alert"
              >
                <XCircle className="mr-1 inline size-3.5" />
                {setupErrorMessage(startDevice.error ?? pollError)}
              </p>
            ) : null}
            <Button
              className="mt-6 h-10 w-full"
              onClick={() => void beginGitHub()}
              disabled={startDevice.isPending}
            >
              {startDevice.isPending ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                <Github className="size-4" />
              )}
              {startDevice.isPending ? "Connecting…" : "Continue with GitHub"}
            </Button>
            <Button
              variant="ghost"
              className="mt-2 w-full"
              onClick={resetToCode}
            >
              Use a different setup code
            </Button>
          </div>
        ) : null}

        {phase === "device" && device ? (
          <div className="space-y-5" aria-live="polite">
            <div className="rounded-xl border border-violet-300/20 bg-violet-300/[0.06] p-5 text-center">
              <p className="text-sm text-slate-400">
                Enter this code on GitHub
              </p>
              <p
                className="mt-3 font-mono text-2xl font-semibold tracking-[0.2em] text-violet-100"
                aria-label={`GitHub device code ${device.user_code}`}
              >
                {device.user_code}
              </p>
              <Button
                variant="outline"
                size="sm"
                className="mt-4"
                onClick={async () => {
                  try {
                    await navigator.clipboard.writeText(device.user_code);
                    setCopyState("copied");
                  } catch {
                    setCopyState("failed");
                  }
                }}
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
                  Copy was unavailable; select the code manually.
                </p>
              ) : null}
            </div>
            <Button asChild className="h-10 w-full">
              <a
                href={device.verification_uri}
                target="_blank"
                rel="noopener noreferrer"
              >
                <ExternalLink className="size-4" />
                Open GitHub
              </a>
            </Button>
            <p className="text-center text-sm text-slate-400" role="status">
              <Loader2 className="mr-1 inline size-4 animate-spin text-cyan-300" />
              Waiting for GitHub authorization…
            </p>
            <p className="text-center font-mono text-sm text-amber-200">
              Expires in {formatRemaining(device.expires_at, now)}
            </p>
            {pollError ? (
              <p
                className="rounded-lg border border-amber-300/20 bg-amber-300/10 px-3 py-2 text-xs leading-5 text-amber-100"
                role="alert"
              >
                {setupErrorMessage(pollError)} Retrying safely.
              </p>
            ) : null}
            <Button
              variant="ghost"
              className="w-full"
              onClick={() => {
                setPhase("verified");
                setDevice(undefined);
              }}
            >
              Cancel GitHub authorization
            </Button>
          </div>
        ) : null}

        <p className="mt-6 text-center text-sm text-slate-500">
          Already completed setup?{" "}
          <Link
            href="/login"
            className="font-medium text-cyan-300 hover:text-cyan-200"
          >
            Sign in
          </Link>
        </p>
      </CardContent>
    </Card>
  );
}
