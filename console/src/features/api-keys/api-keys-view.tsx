"use client";
import { type ColumnDef } from "@tanstack/react-table";
import { useState } from "react";
import { toast } from "sonner";
import { useCreateAPIKey } from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { useProjectAPIKeys } from "@/api/queries";
import type { APIKey } from "@/api/types";
import type { components } from "@/api/generated/schema";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { OneTimeSecretDialog } from "@/components/one-time-secret-dialog";
import { StatusBadge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { pageControls } from "@/lib/pagination";

const defaultScopes =
  "users.read,databases.read,storage.read,functions.read,sites.read,webhooks.read";

export function APIKeysView({ projectId }: { projectId: string }) {
  const navigation = useCursorPagination();
  const query = useProjectAPIKeys(projectId, { cursor: navigation.cursor });
  const create = useCreateAPIKey(projectId);
  const [secret, setSecret] = useState<string | null>(null);
  const handleCreateAPIKey = async (values: Record<string, string>) => {
    const scopes = values.scopes
      .split(",")
      .map((scope) => scope.trim())
      .filter(
        Boolean,
      ) as components["schemas"]["CreateProjectAPIKeyRequest"]["scopes"];
    const result = await create.mutateAsync({ name: values.name, scopes });
    const keyResult = result as
      components["schemas"]["CreateProjectAPIKeyResponse"] | undefined;
    if (keyResult?.secret) setSecret(keyResult.secret);
    create.reset();
    toast.success("API key created");
  };
  const columns: ColumnDef<APIKey, unknown>[] = [
    {
      accessorKey: "name",
      header: "Key",
      cell: ({ row }) => (
        <div>
          <p className="font-medium text-white">{row.original.name}</p>
          <p className="font-mono text-xs text-slate-600">
            {row.original.prefix}••••
          </p>
        </div>
      ),
    },
    {
      accessorKey: "scopes",
      header: "Scopes",
      cell: ({ row }) => (
        <span className="text-xs text-slate-400">
          {row.original.scopes.length} scopes
        </span>
      ),
    },
    {
      accessorKey: "last_used_at",
      header: "Last used",
      cell: ({ row }) => formatDate(row.original.last_used_at),
    },
    {
      accessorKey: "revoked_at",
      header: "State",
      cell: ({ row }) =>
        row.original.revoked_at ? (
          <StatusBadge status="revoked" />
        ) : (
          <StatusBadge status="active" />
        ),
    },
  ];
  if (query.error)
    return <ErrorState error={query.error} retry={() => query.refetch()} />;
  return (
    <>
      <PageHeader
        eyebrow="Integrations"
        title="API keys"
        description="Project-bound keys are managed by the Go API. Secrets are shown once and never persisted by this UI."
        actions={
          <CreateDialog
            title="Create an API key"
            description="Copy the secret before closing this dialog. Stealth will not show it again."
            fields={[
              { name: "name", label: "Name", placeholder: "CI deploy key" },
              {
                name: "scopes",
                label: "Scopes",
                defaultValue: defaultScopes,
                help: "Comma-separated scopes such as functions.write or sites.write.",
              },
            ]}
            pending={create.isPending}
            onSubmit={handleCreateAPIKey}
          />
        }
      />
      <Card>
        <DataTable
          data={query.data?.keys ?? []}
          columns={columns}
          loading={query.isLoading}
          empty="No API keys yet."
          serverPagination={pageControls(
            navigation,
            nextCursor(query.data),
            query.isFetching,
          )}
        />
      </Card>
      <OneTimeSecretDialog
        key={secret ?? "empty-api-key-secret"}
        secret={secret}
        title="Copy this key now"
        description="This secret is returned once by the Go API. Save it before finishing; Stealth will not show it again."
        onDone={() => setSecret(null)}
      />
    </>
  );
}
