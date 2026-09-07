"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { zodResolver } from "@hookform/resolvers/zod";
import {
  CheckCircle2,
  Loader2,
  Mail,
  ShieldCheck,
  XCircle,
} from "lucide-react";
import {
  useConfirmAccountRecovery,
  useConfirmAccountVerification,
  useRecoveryRequest,
} from "@/api/mutations";
import { errorMessage } from "@/components/feedback/error-state";
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

function AuthCard({
  children,
  title,
  description,
}: {
  children: React.ReactNode;
  title: string;
  description: string;
}) {
  return (
    <Card className="w-full max-w-md border-stealth-border/80 bg-stealth-panel">
      <CardHeader className="p-7 pb-4">
        <p className="mb-2 text-xs font-medium uppercase tracking-[0.18em] text-cyan-300">
          Account security
        </p>
        <CardTitle className="text-2xl">{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent className="p-7 pt-2">{children}</CardContent>
    </Card>
  );
}

function tokenPayload(token: string, secret: string | null) {
  return secret ? { secret } : { token };
}

export function AccountVerificationView() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const mutation = useConfirmAccountVerification();
  const attempted = useRef(false);
  const token = searchParams.get("token");
  const secret = searchParams.get("secret");
  const status = searchParams.get("status");

  useEffect(() => {
    if ((!token && !secret) || attempted.current) return;
    attempted.current = true;
    void mutation
      .mutateAsync(tokenPayload(token ?? secret ?? "", secret))
      .then(() => {
        router.replace("/verify?status=success");
      })
      .catch(() => undefined);
  }, [mutation, router, secret, token]);

  if (status === "success") {
    return (
      <AuthCard
        title="Email verified"
        description="Your Console account is ready to use."
      >
        <CheckCircle2 className="size-6 text-emerald-300" />
        <p className="mt-4 text-sm leading-6 text-slate-400">
          Email verification is complete. You can return to the project console.
        </p>
        <Button asChild className="mt-6 w-full">
          <Link href="/organizations">Open console</Link>
        </Button>
      </AuthCard>
    );
  }

  if (!token && !secret) {
    return (
      <AuthCard
        title="Verification link required"
        description="Open the one-time link from your Stealth email to verify this account."
      >
        <XCircle className="size-6 text-amber-300" />
        <p className="mt-4 text-sm leading-6 text-slate-400">
          The link does not contain a usable token. Request another verification
          email from your account page.
        </p>
        <Button asChild variant="outline" className="mt-6 w-full">
          <Link href="/login">Return to sign in</Link>
        </Button>
      </AuthCard>
    );
  }

  return (
    <AuthCard
      title={mutation.error ? "Verification failed" : "Verifying your email"}
      description={
        mutation.error
          ? errorMessage(mutation.error)
          : "Stealth is checking the one-time token with the Go API."
      }
    >
      {mutation.isPending ? (
        <Loader2 className="size-6 animate-spin text-cyan-300" />
      ) : mutation.error ? (
        <>
          <XCircle className="size-6 text-rose-300" />
          <Button
            className="mt-6 w-full"
            onClick={() => {
              mutation.reset();
              attempted.current = false;
            }}
          >
            Try again
          </Button>
        </>
      ) : (
        <Loader2 className="size-6 animate-spin text-cyan-300" />
      )}
    </AuthCard>
  );
}

const recoverySchema = z.object({
  email: z.string().email("Enter a valid email address."),
});
const passwordSchema = z.object({
  password: z.string().min(12, "Use at least 12 characters."),
});

export function PasswordRecoveryView() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const request = useRecoveryRequest();
  const confirm = useConfirmAccountRecovery();
  const token = searchParams.get("token");
  const secret = searchParams.get("secret");
  const hasToken = Boolean(token || secret);
  const [sent, setSent] = useState(false);
  const recoveryForm = useForm<z.infer<typeof recoverySchema>>({
    resolver: zodResolver(recoverySchema),
    defaultValues: { email: "" },
  });
  const passwordForm = useForm<z.infer<typeof passwordSchema>>({
    resolver: zodResolver(passwordSchema),
    defaultValues: { password: "" },
  });

  const requestRecovery = recoveryForm.handleSubmit(async (values) => {
    await request.mutateAsync({ email: values.email });
    setSent(true);
  });
  const resetPassword = passwordForm.handleSubmit(async (values) => {
    await confirm.mutateAsync({
      ...tokenPayload(token ?? secret ?? "", secret),
      password: values.password,
    });
    router.replace("/login?reset=success");
  });

  if (hasToken) {
    return (
      <AuthCard
        title="Choose a new password"
        description="This one-time recovery token is handled by the Go API and is never stored by the console."
      >
        <form onSubmit={resetPassword} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="recovery-password">New password</Label>
            <Input
              id="recovery-password"
              type="password"
              autoComplete="new-password"
              {...passwordForm.register("password")}
            />
            <p className="text-xs text-slate-600">Minimum 12 characters.</p>
            {passwordForm.formState.errors.password ? (
              <p className="text-xs text-rose-300">
                {passwordForm.formState.errors.password.message}
              </p>
            ) : null}
          </div>
          {confirm.error ? (
            <p className="rounded-lg border border-rose-300/20 bg-rose-400/10 px-3 py-2 text-xs leading-5 text-rose-200">
              {errorMessage(confirm.error)}
            </p>
          ) : null}
          <Button className="w-full" disabled={confirm.isPending}>
            {confirm.isPending ? (
              <Loader2 className="size-4 animate-spin" />
            ) : (
              <ShieldCheck className="size-4" />
            )}
            {confirm.isPending ? "Updating password…" : "Set new password"}
          </Button>
        </form>
        <p className="mt-6 text-center text-sm text-slate-500">
          <Link href="/login" className="text-cyan-300 hover:text-cyan-200">
            Back to sign in
          </Link>
        </p>
      </AuthCard>
    );
  }

  return (
    <AuthCard
      title="Reset your password"
      description="We will send a one-time link if the account exists."
    >
      {sent ? (
        <div className="rounded-xl border border-emerald-300/20 bg-emerald-300/10 p-5">
          <CheckCircle2 className="mb-3 size-5 text-emerald-300" />
          <p className="text-sm font-medium text-emerald-100">
            Recovery request accepted
          </p>
          <p className="mt-1 text-xs leading-5 text-emerald-200/70">
            Check your inbox for the one-time recovery link. For account
            privacy, this response is the same whether the address exists.
          </p>
        </div>
      ) : (
        <form onSubmit={requestRecovery} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="recovery-email">Email</Label>
            <Input
              id="recovery-email"
              type="email"
              autoComplete="email"
              {...recoveryForm.register("email")}
            />
            {recoveryForm.formState.errors.email ? (
              <p className="text-xs text-rose-300">
                {recoveryForm.formState.errors.email.message}
              </p>
            ) : null}
          </div>
          {request.error ? (
            <p className="rounded-lg border border-rose-300/20 bg-rose-400/10 px-3 py-2 text-xs leading-5 text-rose-200">
              {errorMessage(request.error)}
            </p>
          ) : null}
          <Button className="w-full" disabled={request.isPending}>
            {request.isPending ? (
              <Loader2 className="size-4 animate-spin" />
            ) : (
              <Mail className="size-4" />
            )}
            {request.isPending ? "Sending…" : "Email recovery link"}
          </Button>
        </form>
      )}
      <p className="mt-6 text-center text-sm text-slate-500">
        <Link
          href="/login"
          className="font-medium text-cyan-300 hover:text-cyan-200"
        >
          Back to sign in
        </Link>
      </p>
    </AuthCard>
  );
}
