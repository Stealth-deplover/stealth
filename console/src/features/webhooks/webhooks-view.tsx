"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
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
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { PageHeader } from "@/components/page-header";
import { OneTimeSecretDialog } from "@/components/one-time-secret-dialog";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { formatDate } from "@/lib/format";
import { pageControls } from "@/lib/pagination";
import { parseCommaSeparatedValues } from "@/features/integrations/integration-values";

export function WebhooksView({
  organizationId,
  projectId,
}: {
  organizationId: string;
  projectId: string;
}) {
  const router = useRouter();
  const navigation = useCursorPagination();
  const query = useWebhooks(projectId, { cursor: navigation.cursor });
  const create = useCreateWebhook(projectId);
  const base = `/organizations/${organizationId}/projects/${projectId}`;
  const [createOpen, setCreateOpen] = useState(false);
  const [secret, setSecret] = useState<string | null>(null);
  const [createdWebhookId, setCreatedWebhookId] = useState<string | null>(null);
  const webhooks = query.data?.webhooks ?? [];
  const canManage = query.data?.can_manage === true;

  const handleCreateWebhook = async (values: Record<string, string>) => {
    const result = await create.mutateAsync({
      name: values.name.trim(),
      url: values.url.trim(),
      events: parseCommaSeparatedValues(values.events),
      enabled: values.enabled !== "false",
    });
    const secretResult = result as
      components["schemas"]["WebhookSecretResponse"] | undefined;
    if (secretResult?.secret) {
      setCreatedWebhookId(secretResult.webhook.id);
      setSecret(secretResult.secret);
    }
    create.reset();
    toast.success("Webhook created");
  };

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
        <span
          className="block max-w-xs truncate font-mono text-xs text-slate-500"
          title={row.original.url}
        >
          {row.original.url}
        </span>
      ),
    },
    {
      accessorKey: "events",
      header: "Events",
      cell: ({ row }) => {
        const events = row.original.events.join(", ");
        return (
          <span
            className="block max-w-sm truncate font-mono text-xs text-slate-400"
            title={events}
          >
            {events || "—"}
          </span>
        );
      },
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
    {
      id: "actions",
      header: "",
      cell: ({ row }) => (
        <Button asChild variant="ghost" size="sm">
          <Link href={`${base}/webhooks/${row.original.id}`}>Open webhook</Link>
        </Button>
      ),
    },
  ];

  const hasRows =
    webhooks.length > 0 || navigation.canFirst || nextCursor(query.data);

  return (
    <>
      <PageHeader
        eyebrow="Integrations"
        title="Webhooks"
        description="Deliver platform events to HTTPS endpoints and inspect delivery metadata."
        actions={
          canManage ? (
            <CreateDialog
              open={createOpen}
              onOpenChange={setCreateOpen}
              triggerLabel="Create webhook"
              submitLabel="Create webhook"
              pendingLabel="Creating webhook…"
              title="Create a webhook"
              description="Copy the signing secret before closing this dialog. Stealth will not show it again."
              fields={[
                {
                  name: "name",
                  label: "Name",
                  placeholder: "production-events",
                },
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
                  help: "Comma-separated event names, or * for all. The Go API validates event names.",
                },
                {
                  name: "enabled",
                  label: "State",
                  type: "select",
                  defaultValue: "true",
                  options: [
                    { value: "true", label: "Enabled" },
                    { value: "false", label: "Disabled" },
                  ],
                },
              ]}
              pending={create.isPending}
              onSubmit={handleCreateWebhook}
            />
          ) : query.data ? (
            <Badge variant="neutral">Read-only</Badge>
          ) : null
        }
      />
      {query.isError ? (
        <ErrorState
          title="Could not load webhooks"
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
            data={webhooks}
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
          title="No webhooks yet"
          description="Create a webhook to send Stealth events to another service."
          action={canManage ? () => setCreateOpen(true) : undefined}
          actionLabel={canManage ? "Create webhook" : undefined}
        />
      )}
      <OneTimeSecretDialog
        key={secret ?? "empty-webhook-secret"}
        secret={secret}
        title="Copy this signing secret now"
        description="This secret is returned once by the Go API. Save it before finishing; Stealth will not show it again."
        onDone={() => {
          setSecret(null);
          const webhookId = createdWebhookId;
          setCreatedWebhookId(null);
          if (webhookId) router.push(`${base}/webhooks/${webhookId}`);
        }}
      />
    </>
  );
}
