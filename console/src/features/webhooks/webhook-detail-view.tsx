"use client";
import type { ColumnDef } from "@tanstack/react-table";
import { useState } from "react";
import { toast } from "sonner";
import { nextCursor } from "@/api/pagination";
import { useRotateWebhookSecret } from "@/api/mutations";
import { useWebhook, useWebhookDeliveries } from "@/api/queries";
import type { WebhookDelivery } from "@/api/types";
import type { components } from "@/api/generated/schema";
import { DataTable } from "@/components/data-table";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { OneTimeSecretDialog } from "@/components/one-time-secret-dialog";
import { PageHeader } from "@/components/page-header";
import { CopyButton } from "@/components/copy-button";
import { ResourceId } from "@/components/resource-id";
import { StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
import { pageControls } from "@/lib/pagination";
import { BackLink } from "@/features/resources/detail-shared";

export function WebhookDetailView({
  organizationId,
  projectId,
  webhookId,
}: {
  organizationId: string;
  projectId: string;
  webhookId: string;
}) {
  const webhook = useWebhook(projectId, webhookId);
  const deliveriesNavigation = useCursorPagination("deliveries_cursor");
  const query = useWebhookDeliveries(projectId, webhookId, {
    cursor: deliveriesNavigation.cursor,
  });
  const rotate = useRotateWebhookSecret(projectId, webhookId);
  const [secret, setSecret] = useState<string | null>(null);
  const columns: ColumnDef<WebhookDelivery, unknown>[] = [
    {
      accessorKey: "event_name",
      header: "Event",
      cell: ({ row }) => (
        <span className="font-mono text-xs text-white">
          {row.original.event_name}
        </span>
      ),
    },
    {
      accessorKey: "status",
      header: "Status",
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: "last_status_code",
      header: "HTTP",
      cell: ({ row }) => row.original.last_status_code ?? "—",
    },
    { accessorKey: "attempt_count", header: "Attempts" },
    {
      accessorKey: "created_at",
      header: "Created",
      cell: ({ row }) => formatDate(row.original.created_at),
    },
  ];
  if (webhook.error)
    return <ErrorState error={webhook.error} retry={() => webhook.refetch()} />;
  if (query.error)
    return <ErrorState error={query.error} retry={() => query.refetch()} />;
  const current = webhook.data?.webhook;
  if (!current)
    return (
      <EmptyState
        title="Webhook not found"
        description="The webhook may have been removed or is outside this project."
      />
    );
  return (
    <>
      <BackLink
        href={`/organizations/${organizationId}/projects/${projectId}/webhooks`}
        label="Back to webhooks"
      />
      <PageHeader
        eyebrow="Webhook"
        title={current.name}
        description="Deliver platform events and inspect delivery history."
        actions={
          <div className="flex items-center gap-2">
            <StatusBadge status={current.enabled ? "active" : "inactive"} />
            <ConfirmDialog
              trigger={<Button variant="outline">Rotate secret</Button>}
              title="Rotate this webhook secret?"
              description="The current signing secret will stop working for future deliveries. Save the new secret before finishing; it will only be returned once."
              confirmLabel="Rotate secret"
              pending={rotate.isPending}
              onConfirm={async () => {
                const result = await rotate.mutateAsync();
                const secretResult = result as
                  components["schemas"]["WebhookSecretResponse"] | undefined;
                if (secretResult?.secret) setSecret(secretResult.secret);
                rotate.reset();
                toast.success("Webhook secret rotated");
              }}
            />
          </div>
        }
      />
      <div className="mb-5 flex flex-wrap items-center gap-2">
        <span className="text-xs text-slate-600">
          Updated {formatDate(current.updated_at)}
        </span>
        <ResourceId id={current.id} label="Webhook ID" />
      </div>
      <Card className="mb-5">
        <CardHeader>
          <CardTitle>Configuration</CardTitle>
        </CardHeader>
        <CardContent>
          <dl className="grid gap-4 md:grid-cols-2">
            <div>
              <dt className="text-xs text-slate-600">Endpoint</dt>
              <dd className="mt-1 flex items-start gap-1 break-all font-mono text-xs text-slate-300">
                <span>{current.url}</span>
                <CopyButton
                  value={current.url}
                  label="Copy webhook URL"
                  className="size-6 shrink-0 text-slate-600 hover:text-slate-200"
                />
              </dd>
            </div>
            <div>
              <dt className="text-xs text-slate-600">Events</dt>
              <dd className="mt-1 text-sm text-slate-300">
                {current.events.join(", ")}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-slate-600">Secret state</dt>
              <dd className="mt-1 text-sm text-slate-300">
                Configured · secret withheld
              </dd>
            </div>
            <div>
              <dt className="text-xs text-slate-600">Failures</dt>
              <dd className="mt-1 text-sm text-slate-300">
                {current.failure_count}
              </dd>
            </div>
          </dl>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Delivery history</CardTitle>
          <p className="mt-1 text-xs text-slate-500">
            The backend returns bounded delivery metadata. Request and response
            bodies are not exposed by this contract.
          </p>
        </CardHeader>
        <DataTable
          data={query.data?.deliveries ?? []}
          columns={columns}
          loading={query.isLoading}
          empty="No deliveries yet. Delivery attempts will appear here after an event is sent."
          serverPagination={pageControls(
            deliveriesNavigation,
            nextCursor(query.data),
            query.isFetching,
          )}
        />
      </Card>
      <OneTimeSecretDialog
        key={secret ?? "empty-webhook-rotation-secret"}
        secret={secret}
        title="Copy the new signing secret now"
        description="This rotated secret is returned once by the Go API. Save it before finishing; Stealth will not show it again."
        onDone={() => setSecret(null)}
      />
    </>
  );
}
