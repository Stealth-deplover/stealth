"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { useAccountSessions, useCurrentAccount } from "@/api/queries";
import {
  useLogout,
  useRevokeAccountSession,
  useRevokeOtherAccountSessions,
  useSendAccountVerification,
  useUpdateAccountPassword,
} from "@/api/mutations";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { formatDate } from "@/lib/format";

export function AccountView() {
  const router = useRouter();
  const account = useCurrentAccount();
  const sessions = useAccountSessions();
  const revoke = useRevokeAccountSession();
  const revokeOthers = useRevokeOtherAccountSessions();
  const updatePassword = useUpdateAccountPassword();
  const sendVerification = useSendAccountVerification();
  const logout = useLogout();
  const [passwords, setPasswords] = useState({
    current_password: "",
    password: "",
  });
  const current = account.data?.account;

  if (account.error)
    return (
      <ErrorState
        title="Could not load account"
        error={account.error}
        retry={() => account.refetch()}
      />
    );
  if (!current) return null;

  const submitPassword = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    await updatePassword.mutateAsync(passwords);
    setPasswords({ current_password: "", password: "" });
    toast.success("Password updated; other sessions were revoked");
    await sessions.refetch();
  };

  return (
    <>
      <PageHeader
        eyebrow="Account"
        title="Personal access"
        description="Manage your Console identity, active sessions, and password without storing secrets in the browser."
      />
      <div className="grid gap-5 xl:grid-cols-[.8fr_1.2fr]">
        <Card>
          <CardHeader>
            <CardTitle>Identity</CardTitle>
          </CardHeader>
          <CardContent>
            <dl className="space-y-4 text-sm">
              <div>
                <dt className="text-xs text-slate-600">Email</dt>
                <dd className="mt-1 text-white">{current.email}</dd>
              </div>
              <div>
                <dt className="text-xs text-slate-600">Account ID</dt>
                <dd className="mt-1 break-all font-mono text-xs text-slate-400">
                  {current.id}
                </dd>
              </div>
              <div>
                <dt className="text-xs text-slate-600">Created</dt>
                <dd className="mt-1 text-slate-300">
                  {formatDate(current.created_at)}
                </dd>
              </div>
              <div>
                <dt className="text-xs text-slate-600">Email verification</dt>
                <dd className="mt-1 flex flex-wrap items-center gap-2">
                  <StatusBadge
                    status={current.email_verified ? "verified" : "warning"}
                  />
                  {!current.email_verified ? (
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={sendVerification.isPending}
                      onClick={() =>
                        sendVerification.mutate(
                          {},
                          {
                            onSuccess: () =>
                              toast.success("Verification email sent"),
                            onError: (error) =>
                              toast.error(
                                error instanceof Error
                                  ? error.message
                                  : "Unable to send verification email",
                              ),
                          },
                        )
                      }
                    >
                      {sendVerification.isPending ? "Sending…" : "Send again"}
                    </Button>
                  ) : null}
                </dd>
              </div>
            </dl>
            <Button
              variant="outline"
              className="mt-6 w-full"
              disabled={logout.isPending}
              onClick={() =>
                logout.mutate(undefined, {
                  onSuccess: () => router.replace("/login"),
                })
              }
            >
              Sign out
            </Button>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Change password</CardTitle>
          </CardHeader>
          <CardContent>
            <form onSubmit={submitPassword} className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="current-password">Current password</Label>
                <Input
                  id="current-password"
                  type="password"
                  required
                  value={passwords.current_password}
                  onChange={(event) =>
                    setPasswords((value) => ({
                      ...value,
                      current_password: event.target.value,
                    }))
                  }
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="new-password">New password</Label>
                <Input
                  id="new-password"
                  type="password"
                  minLength={12}
                  required
                  value={passwords.password}
                  onChange={(event) =>
                    setPasswords((value) => ({
                      ...value,
                      password: event.target.value,
                    }))
                  }
                />
                <p className="text-xs text-slate-600">Minimum 12 characters.</p>
              </div>
              <p className="text-xs leading-5 text-slate-500">
                The Go API keeps the current session and revokes all other
                Console sessions after a successful change.
              </p>
              <Button type="submit" disabled={updatePassword.isPending}>
                {updatePassword.isPending ? "Updating…" : "Update password"}
              </Button>
            </form>
          </CardContent>
        </Card>
      </div>
      <Card className="mt-5">
        <CardHeader className="flex-row items-center justify-between">
          <div>
            <CardTitle>Active sessions</CardTitle>
            <p className="mt-1 text-xs text-slate-500">
              Session tokens and hashes are never returned by the API.
            </p>
          </div>
          <Button
            variant="outline"
            size="sm"
            disabled={revokeOthers.isPending}
            onClick={() =>
              revokeOthers.mutate(undefined, {
                onSuccess: (result) =>
                  toast.success(
                    `${result?.revoked ?? 0} other sessions revoked`,
                  ),
              })
            }
          >
            Revoke others
          </Button>
        </CardHeader>
        <CardContent className="space-y-2">
          {sessions.error ? (
            <ErrorState
              error={sessions.error}
              retry={() => sessions.refetch()}
            />
          ) : sessions.data?.sessions.length ? (
            sessions.data.sessions.map((session) => (
              <div
                key={session.id}
                className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-stealth-border p-3"
              >
                <div>
                  <p className="font-mono text-xs text-slate-300">
                    {session.id}
                  </p>
                  <p className="mt-1 text-xs text-slate-600">
                    Created {formatDate(session.created_at)} · expires{" "}
                    {formatDate(session.expires_at)}
                  </p>
                </div>
                <div className="flex items-center gap-2">
                  {session.is_current ? (
                    <StatusBadge status="current" />
                  ) : (
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={revoke.isPending}
                      onClick={() =>
                        revoke.mutate(session.id, {
                          onSuccess: () => toast.success("Session revoked"),
                        })
                      }
                    >
                      Revoke
                    </Button>
                  )}
                </div>
              </div>
            ))
          ) : sessions.isLoading ? (
            <p className="text-sm text-slate-500">Loading sessions…</p>
          ) : (
            <p className="text-sm text-slate-500">
              No active sessions returned.
            </p>
          )}
        </CardContent>
      </Card>
    </>
  );
}
