"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { type ColumnDef } from "@tanstack/react-table";
import { useState } from "react";
import { toast } from "sonner";
import { CreateProjectAPIKeyRequestScopes } from "@/api/generated/schema";
import { useCreateAPIKey, useRevokeAPIKey } from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { useProjectAPIKeys } from "@/api/queries";
import type { APIKey } from "@/api/types";
import type { components } from "@/api/generated/schema";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { OneTimeSecretDialog } from "@/components/one-time-secret-dialog";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import {
  formatAPIKeyScope,
  parseCommaSeparatedValues,
} from "@/features/integrations/integration-values";
import { getApiKeyStatus } from "@/features/api-keys/api-key-status";
import { formatDate } from "@/lib/format";
import { pageControls } from "@/lib/pagination";

const scopeOptions = Object.values(CreateProjectAPIKeyRequestScopes).map(
  (scope) => ({
    value: scope,
    label: formatAPIKeyScope(scope),
  }),
);

function APIKeyRevokeAction({
  projectId,
  apiKey,
}: {
  projectId: string;
  apiKey: APIKey;
}) {
  const revoke = useRevokeAPIKey(projectId, apiKey.id);
  return (
    <ConfirmDialog
      trigger={
        <Button variant="ghost" size="sm" disabled={revoke.isPending}>
          Revoke
        </Button>
      }
      title="Revoke API key?"
      description="Applications using this key will no longer be able to authenticate."
      confirmLabel="Revoke key"
      pending={revoke.isPending}
      onConfirm={async () => {
        await revoke.mutateAsync();
        toast.success("API key revoked");
      }}
    />
  );
}

export function APIKeysView({
  organizationId,
  projectId,
}: {
  organizationId: string;
  projectId: string;
}) {
  const router = useRouter();
  const navigation = useCursorPagination();
  const query = useProjectAPIKeys(projectId, { cursor: navigation.cursor });
  const create = useCreateAPIKey(projectId);
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const [createOpen, setCreateOpen] = useState(false);
  const [secret, setSecret] = useState<string | null>(null);
  const [createdKeyId, setCreatedKeyId] = useState<string | null>(null);
  const keys = query.data?.keys ?? [];
  const canManage = query.data?.can_manage === true;

  const handleCreateAPIKey = async (values: Record<string, string>) => {
    const scopes = parseCommaSeparatedValues(
      values.scopes,
    ) as components["schemas"]["CreateProjectAPIKeyRequest"]["scopes"];
    if (!scopes.length) throw new Error("Select at least one permission.");
    const expiresAt = values.expires_at
      ? new Date(values.expires_at)
      : undefined;
    if (expiresAt && Number.isNaN(expiresAt.valueOf()))
      throw new Error("Enter a valid expiry date.");
    const result = await create.mutateAsync({
      name: values.name.trim(),
      scopes,
      expires_at: expiresAt?.toISOString() ?? null,
    });
    const keyResult = result as
      components["schemas"]["CreateProjectAPIKeyResponse"] | undefined;
    if (keyResult?.secret) {
      setCreatedKeyId(keyResult.key.id);
      setSecret(keyResult.secret);
    }
    create.reset();
    toast.success("API key created");
  };

  const columns: ColumnDef<APIKey, unknown>[] = [
    {
      accessorKey: "name",
      header: "Key",
      cell: ({ row }) => (
        <div>
          <Link
            href={`${base}/api-keys/${row.original.id}`}
            className="font-medium text-white hover:text-cyan-200"
          >
            {row.original.name}
          </Link>
          <p className="font-mono text-xs text-slate-600">
            {row.original.prefix}
          </p>
        </div>
      ),
    },
    {
      accessorKey: "scopes",
      header: "Permissions",
      cell: ({ row }) => {
        const scopes = row.original.scopes.map(formatAPIKeyScope).join(", ");
        return (
          <span
            className="block max-w-sm truncate text-xs text-slate-400"
            title={scopes}
          >
            {scopes || "—"}
          </span>
        );
      },
    },
    {
      accessorKey: "created_at",
      header: "Created",
      cell: ({ row }) => formatDate(row.original.created_at),
    },
    {
      accessorKey: "last_used_at",
      header: "Last used",
      cell: ({ row }) => formatDate(row.original.last_used_at),
    },
    {
      accessorKey: "expires_at",
      header: "Expires",
      cell: ({ row }) => formatDate(row.original.expires_at),
    },
    {
      accessorKey: "revoked_at",
      header: "State",
      cell: ({ row }) => <StatusBadge status={getApiKeyStatus(row.original)} />,
    },
    {
      id: "actions",
      header: "",
      cell: ({ row }) => {
        const status = getApiKeyStatus(row.original);
        return canManage && status !== "revoked" ? (
          <APIKeyRevokeAction projectId={projectId} apiKey={row.original} />
        ) : null;
      },
    },
  ];

  const hasRows =
    keys.length > 0 || navigation.canFirst || nextCursor(query.data);

  return (
    <>
      <PageHeader
        eyebrow="Integrations"
        title="API keys"
        description="Project-bound keys authenticate integrations. Secrets are shown once by the Go API and never persisted by this UI."
        actions={
          canManage ? (
            <CreateDialog
              open={createOpen}
              onOpenChange={setCreateOpen}
              triggerLabel="Create API key"
              submitLabel="Create API key"
              pendingLabel="Creating API key…"
              title="Create an API key"
              description="Copy the secret before closing this dialog. Stealth will not show it again."
              fields={[
                { name: "name", label: "Name", placeholder: "CI deploy key" },
                {
                  name: "scopes",
                  label: "Permissions",
                  type: "multiselect",
                  options: scopeOptions,
                  help: "Select the exact API scopes this integration needs. Read and write access are separate permissions.",
                },
                {
                  name: "expires_at",
                  label: "Expires at",
                  type: "datetime-local",
                  required: false,
                  help: "Optional. The Go API accepts future expiries within its configured limit.",
                },
              ]}
              pending={create.isPending}
              onSubmit={handleCreateAPIKey}
            />
          ) : query.data ? (
            <Badge variant="neutral">Read-only</Badge>
          ) : null
        }
      />
      {query.error ? (
        <ErrorState
          title="Could not load API keys"
          error={query.error}
          retry={() => query.refetch()}
        />
      ) : query.isPending ? (
        <Card>
          <DataTable data={[]} columns={columns} loading />
        </Card>
      ) : hasRows ? (
        <Card>
          <DataTable
            data={keys}
            columns={columns}
            serverPagination={pageControls(
              navigation,
              nextCursor(query.data),
              query.isFetching,
            )}
          />
        </Card>
      ) : (
        <EmptyState
          title="No API keys yet"
          description="Create a key to authenticate a project integration or deployment."
          action={canManage ? () => setCreateOpen(true) : undefined}
          actionLabel={canManage ? "Create API key" : undefined}
        />
      )}
      <OneTimeSecretDialog
        key={secret ?? "empty-api-key-secret"}
        secret={secret}
        title="Copy this key now"
        description="This secret is returned once by the Go API. Save it before finishing; Stealth will not show it again."
        onDone={() => {
          setSecret(null);
          const keyId = createdKeyId;
          setCreatedKeyId(null);
          if (keyId) router.push(`${base}/api-keys/${keyId}`);
        }}
      />
    </>
  );
}
