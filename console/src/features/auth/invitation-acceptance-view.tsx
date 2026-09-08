"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useState } from "react";
import { CheckCircle2, Loader2, ShieldCheck, XCircle } from "lucide-react";
import { useAcceptOrganizationInvitation } from "@/api/mutations";
import { ApiError } from "@/api/client";
import { errorMessage } from "@/components/feedback/error-state";
import { Button } from "@/components/ui/button";
import { AuthCard } from "@/features/auth/auth-flow-views";
import type { Membership } from "@/api/types";

const tokenPattern = /^[A-Za-z0-9_-]{43}$/;

function loginHref(token: string | null, secret: string | null) {
  const invitation = new URLSearchParams();
  if (token) invitation.set("token", token);
  if (secret) invitation.set("secret", secret);
  const next = "/accept-invitation?" + invitation.toString();
  return "/login?next=" + encodeURIComponent(next);
}

export function InvitationAcceptanceView() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const accept = useAcceptOrganizationInvitation();
  const [membership, setMembership] = useState<Membership>();
  const token = searchParams.get("token");
  const secret = searchParams.get("secret");
  const credential = token ? { token } : secret ? { secret } : undefined;
  const validCredential = Boolean(
    credential &&
    (tokenPattern.test(token ?? "") || tokenPattern.test(secret ?? "")),
  );

  if (membership) {
    return (
      <AuthCard
        title="Invitation accepted"
        description="The Go API created your organization membership."
      >
        <CheckCircle2 className="size-6 text-emerald-300" />
        <p className="mt-4 text-sm leading-6 text-slate-400">
          Organization access is now available for your signed-in account.
        </p>
        <Button
          className="mt-6 w-full"
          onClick={() =>
            router.replace("/organizations/" + membership.organization_id)
          }
        >
          Open organization
        </Button>
      </AuthCard>
    );
  }

  if (!validCredential) {
    return (
      <AuthCard
        title="Invitation link required"
        description="Open the one-time invitation link from your Stealth email."
      >
        <XCircle className="size-6 text-amber-300" />
        <p className="mt-4 text-sm leading-6 text-slate-400">
          The link does not contain a usable invitation credential.
        </p>
        <Button asChild variant="outline" className="mt-6 w-full">
          <Link href="/login">Return to sign in</Link>
        </Button>
      </AuthCard>
    );
  }

  const unauthenticated =
    accept.error instanceof ApiError && accept.error.status === 401;

  return (
    <AuthCard
      title={
        accept.error ? "Invitation could not be accepted" : "Review invitation"
      }
      description={
        accept.error
          ? errorMessage(accept.error)
          : "The Go API will verify your signed-in email and consume this one-time invitation."
      }
    >
      {accept.error ? (
        <XCircle className="size-6 text-rose-300" />
      ) : (
        <ShieldCheck className="size-6 text-cyan-300" />
      )}
      <p className="mt-4 text-sm leading-6 text-slate-400">
        Accepting adds the signed-in account to the organization with the role
        granted by the sender. The invitation token is sent only to the Go API.
      </p>
      {accept.error ? (
        <div className="mt-6 space-y-3">
          {unauthenticated ? (
            <Button asChild className="w-full">
              <Link href={loginHref(token, secret)}>Sign in to accept</Link>
            </Button>
          ) : (
            <Button className="w-full" onClick={() => accept.reset()}>
              Try again
            </Button>
          )}
          <Button asChild variant="ghost" className="w-full">
            <Link href="/organizations">Open console</Link>
          </Button>
        </div>
      ) : (
        <Button
          className="mt-6 w-full"
          disabled={accept.isPending}
          onClick={async () => {
            const result = await accept.mutateAsync(credential);
            if (result?.membership) setMembership(result.membership);
          }}
        >
          {accept.isPending ? (
            <Loader2 className="size-4 animate-spin" />
          ) : (
            <ShieldCheck className="size-4" />
          )}
          {accept.isPending ? "Accepting…" : "Accept invitation"}
        </Button>
      )}
    </AuthCard>
  );
}
