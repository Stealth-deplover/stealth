"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { useRevokeAPIKey } from "@/api/mutations";
import { useProjectAPIKey, useProjectAPIKeys } from "@/api/queries";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { formatAPIKeyScope } from "@/features/integrations/integration-values";
import { getApiKeyStatus } from "@/features/api-keys/api-key-status";
import { formatDate } from "@/lib/format";
import { BackLink } from "@/features/resources/detail-shared";

export function APIKeyDetailView({
  organizationId,
  projectId,
  keyId,
}: {
  organizationId: string;
  projectId: string;
  keyId: string;
}) {
  const router = useRouter();
  const query = useProjectAPIKey(projectId, keyId);
  const permissions = useProjectAPIKeys(projectId);
  const revoke = useRevokeAPIKey(projectId, keyId);
  const key = query.data?.key;
  const canManage = permissions.data?.can_manage === true;
  const base = `/organizations/${organizationId}/projects/${projectId}`;

  if (query.isPending) return <LoadingState rows={4} />;
  if (query.error && !key)
    return (
      <ErrorState
        title="Could not load API key"
        error={query.error}
        retry={() => query.refetch()}
      />
    );
  if (!key)
    return (
      <EmptyState
        title="API key not found"
        description="This key may have been removed or is outside this project."
      />
    );

  const status = getApiKeyStatus(key);

  const handleRevoke = async () => {
    await revoke.mutateAsync();
    toast.success("API key revoked");
    router.replace(`${base}/api-keys`);
  };

  return (
    <>
      <BackLink href={`${base}/api-keys`} label="Back to API keys" />
      <PageHeader
        eyebrow="API key"
        title={key.name}
        description="Safe key metadata from the Go API. The secret cannot be recovered after creation."
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <StatusBadge status={status} />
            {canManage && status !== "revoked" ? (
              <ConfirmDialog
                trigger={
                  <Button variant="destructive" disabled={revoke.isPending}>
                    Revoke key
                  </Button>
                }
                title="Revoke API key?"
                description="Applications using this key will no longer be able to authenticate."
                confirmLabel="Revoke key"
                pending={revoke.isPending}
                onConfirm={handleRevoke}
              />
            ) : permissions.data ? (
              <Badge variant="neutral">Read-only</Badge>
            ) : null}
          </div>
        }
      />
      <div className="mb-5 flex flex-wrap items-center gap-3 text-xs text-slate-500">
        <ResourceId id={key.id} label="API key ID" />
        <span>Created {formatDate(key.created_at)}</span>
        <span>Updated {formatDate(key.updated_at)}</span>
      </div>
      {query.error ? (
        <ErrorState
          title="Could not refresh API key"
          error={query.error}
          retry={() => query.refetch()}
        />
      ) : null}
      {permissions.error ? (
        <ErrorState
          title="Could not load API key permissions"
          error={permissions.error}
          retry={() => permissions.refetch()}
        />
      ) : null}
      <div className="grid gap-5 lg:grid-cols-[1.15fr_.85fr]">
        <Card>
          <CardHeader>
            <CardTitle>Key metadata</CardTitle>
          </CardHeader>
          <CardContent>
            <dl className="grid gap-5 sm:grid-cols-2">
              <div>
                <dt className="text-xs text-slate-500">Prefix</dt>
                <dd className="mt-1 font-mono text-sm text-white">
                  {key.prefix}
                </dd>
              </div>
              <div>
                <dt className="text-xs text-slate-500">Status</dt>
                <dd className="mt-1">
                  <StatusBadge status={status} />
                </dd>
              </div>
              <div>
                <dt className="text-xs text-slate-500">Last used</dt>
                <dd className="mt-1 text-sm text-slate-300">
                  {formatDate(key.last_used_at)}
                </dd>
              </div>
              <div>
                <dt className="text-xs text-slate-500">Expires</dt>
                <dd className="mt-1 text-sm text-slate-300">
                  {formatDate(key.expires_at)}
                </dd>
              </div>
              {status === "revoked" ? (
                <div>
                  <dt className="text-xs text-slate-500">Revoked</dt>
                  <dd className="mt-1 text-sm text-slate-300">
                    {formatDate(key.revoked_at)}
                  </dd>
                </div>
              ) : null}
            </dl>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Permissions</CardTitle>
          </CardHeader>
          <CardContent>
            <ul className="space-y-2">
              {key.scopes.map((scope) => (
                <li
                  key={scope}
                  className="flex items-center justify-between gap-3 rounded-md border border-stealth-border px-3 py-2"
                >
                  <span className="text-sm text-slate-200">
                    {formatAPIKeyScope(scope)}
                  </span>
                  <code className="font-mono text-[10px] text-slate-600">
                    {scope}
                  </code>
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      </div>
      <Card className="mt-5">
        <CardHeader>
          <CardTitle>Secret handling</CardTitle>
        </CardHeader>
        <CardContent className="text-sm leading-6 text-slate-400">
          The full secret was returned only by the create response. This page
          intentionally shows metadata and the safe prefix only; it does not
          read from or write to browser storage.
        </CardContent>
      </Card>
      <Button asChild variant="ghost" className="mt-5">
        <Link href={`${base}/api-keys`}>Return to API keys</Link>
      </Button>
    </>
  );
}
