"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { zodResolver } from "@hookform/resolvers/zod";
import {
  ArrowRight,
  CheckCircle2,
  Loader2,
  ShieldCheck,
  XCircle,
} from "lucide-react";
import { ApiError } from "@/api/client";
import { useCreateInstanceOwner } from "@/api/mutations";
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
const schema = z.object({
  setup_code: z
    .string()
    .trim()
    .regex(setupCodePattern, "Enter the setup code shown by the Stealth CLI."),
  email: z.string().email("Enter a valid email address."),
  password: z.string().min(12, "Use at least 12 characters."),
});
type FormValues = z.infer<typeof schema>;

function setupErrorMessage(error: unknown) {
  if (error instanceof ApiError) {
    if (error.code === "invalid_bootstrap_code") {
      return "That setup code is invalid, expired, or already used.";
    }
    if (error.code === "bootstrap_complete") {
      return "Instance setup has already been completed. Sign in to continue.";
    }
  }
  return errorMessage(error);
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
  const mutation = useCreateInstanceOwner();
  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { setup_code: "", email: "", password: "" },
  });

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

  if (!status.data?.setup_required) {
    return <CompletedView />;
  }

  const submit = form.handleSubmit(async (values) => {
    await mutation.mutateAsync(values);
    router.replace("/organizations");
  });

  return (
    <Card className="w-full max-w-md border-stealth-border/80 bg-stealth-panel">
      <CardHeader className="p-7 pb-4">
        <p className="mb-2 text-xs font-medium uppercase tracking-[0.18em] text-cyan-300">
          First-run security
        </p>
        <CardTitle className="text-2xl">Create your Stealth owner</CardTitle>
        <CardDescription>
          Enter the one-time code from the CLI, then choose the credentials for
          the first Instance Owner.
        </CardDescription>
      </CardHeader>
      <CardContent className="p-7 pt-2">
        <div className="mb-5 rounded-xl border border-cyan-300/20 bg-cyan-300/[0.06] p-4 text-sm leading-6 text-slate-300">
          <ShieldCheck className="mb-2 size-5 text-cyan-300" />
          The setup code is sent directly to the Go API, is never placed in the
          URL, and can be used only once.
        </div>
        <form onSubmit={submit} className="space-y-4" noValidate>
          <div className="space-y-2">
            <Label htmlFor="setup-code">Setup code</Label>
            <Input
              id="setup-code"
              type="text"
              inputMode="text"
              autoComplete="one-time-code"
              spellCheck={false}
              translate="no"
              {...form.register("setup_code")}
            />
            {form.formState.errors.setup_code ? (
              <p className="text-xs text-rose-300">
                {form.formState.errors.setup_code.message}
              </p>
            ) : null}
          </div>
          <div className="space-y-2">
            <Label htmlFor="owner-email">Email</Label>
            <Input
              id="owner-email"
              type="email"
              autoComplete="email"
              spellCheck={false}
              {...form.register("email")}
            />
            {form.formState.errors.email ? (
              <p className="text-xs text-rose-300">
                {form.formState.errors.email.message}
              </p>
            ) : null}
          </div>
          <div className="space-y-2">
            <Label htmlFor="owner-password">Password</Label>
            <Input
              id="owner-password"
              type="password"
              autoComplete="new-password"
              {...form.register("password")}
            />
            <p className="text-xs text-slate-600">Minimum 12 characters.</p>
            {form.formState.errors.password ? (
              <p className="text-xs text-rose-300">
                {form.formState.errors.password.message}
              </p>
            ) : null}
          </div>
          {mutation.error ? (
            <p
              className="rounded-lg border border-rose-300/20 bg-rose-400/10 px-3 py-2 text-xs leading-5 text-rose-200"
              role="alert"
              aria-live="polite"
            >
              <XCircle className="mr-1 inline size-3.5" />
              {setupErrorMessage(mutation.error)}
            </p>
          ) : null}
          <Button className="mt-2 h-10 w-full" disabled={mutation.isPending}>
            {mutation.isPending ? (
              <Loader2 className="size-4 animate-spin" />
            ) : (
              <ArrowRight className="size-4" />
            )}
            {mutation.isPending ? "Creating owner…" : "Create Instance Owner"}
          </Button>
        </form>
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
