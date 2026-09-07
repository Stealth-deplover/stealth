"use client";
import Link from "next/link";
import { type ColumnDef } from "@tanstack/react-table";
import { useState } from "react";
import { toast } from "sonner";
import { useCreateWebhook } from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { useWebhooks } from "@/api/queries";
import type { Webhook } from "@/api/types";
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

export function WebhooksView({
  organizationId,
  projectId,
}: {
  organizationId: string;
  projectId: string;
}) {
  const navigation = useCursorPagination();
  const query = useWebhooks(projectId, { cursor: navigation.cursor });
  const create = useCreateWebhook(projectId);
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const columns: ColumnDef<Webhook, unknown>[] = [
    {
      accessorKey: "name",
      header: "Webhook",
      cell: ({ row }) => (
        <Link
          href={`${base}/webhooks/${row.original.id}`}
          className="font-medium text-white hover:text-pink-200"
        >
          {row.original.name}
        </Link>
      ),
    },
    {
      accessorKey: "url",
      header: "Endpoint",
      cell: ({ row }) => (
        <span className="max-w-xs truncate font-mono text-xs text-slate-500">
          {row.original.url}
        </span>
      ),
    },
    {
      accessorKey: "enabled",
      header: "State",
      cell: ({ row }) => (
        <StatusBadge status={row.original.enabled ? "active" : "inactive"} />
      ),
    },
    {
      accessorKey: "failure_count",
      header: "Failures",
      cell: ({ row }) => row.original.failure_count,
    },
    {
      accessorKey: "last_delivery_at",
      header: "Last delivery",
      cell: ({ row }) => formatDate(row.original.last_delivery_at),
    },
  ];
  const [secret, setSecret] = useState<string | null>(null);
  const handleCreateWebhook = async (values: Record<string, string>) => {
    const result = await create.mutateAsync({
      name: values.name,
      url: values.url,
      events: values.events
        .split(",")
        .map((event) => event.trim())
        .filter(Boolean),
      enabled: true,
    });
    const secretResult = result as
      components["schemas"]["WebhookSecretResponse"] | undefined;
    if (secretResult?.secret) setSecret(secretResult.secret);
    create.reset();
    toast.success("Webhook created");
  };
  return (
    <>
      <PageHeader
        eyebrow="Integrations"
        title="Webhooks"
        description="Deliver platform events to HTTPS endpoints and inspect delivery metadata."
        actions={
          <CreateDialog
            triggerLabel="Create webhook"
            submitLabel="Create webhook"
            pendingLabel="Creating webhook…"
            title="Create a webhook"
            description="Copy the signing secret before closing this dialog. Stealth will not show it again."
            fields={[
              { name: "name", label: "Name", placeholder: "production-events" },
              {
                name: "url",
                label: "HTTPS URL",
                type: "url",
                placeholder: "https://example.com/hooks",
              },
              {
                name: "events",
                label: "Events",
                required: false,
                defaultValue: "*",
                help: "Comma-separated event names, or * for all.",
              },
            ]}
            pending={create.isPending}
            onSubmit={handleCreateWebhook}
          />
        }
      />
      {query.isError ? (
        <ErrorState error={query.error} retry={() => query.refetch()} />
      ) : (
        <Card>
          <DataTable
            data={query.data?.webhooks ?? []}
            columns={columns}
            loading={query.isLoading}
            empty="No webhooks yet. Create one to start receiving project events."
            serverPagination={pageControls(
              navigation,
              nextCursor(query.data),
              query.isFetching,
            )}
          />
        </Card>
      )}
      <OneTimeSecretDialog
        key={secret ?? "empty-webhook-secret"}
        secret={secret}
        title="Copy this signing secret now"
        description="This secret is returned once by the Go API. Save it before finishing; Stealth will not show it again."
        onDone={() => setSecret(null)}
      />
    </>
  );
}
