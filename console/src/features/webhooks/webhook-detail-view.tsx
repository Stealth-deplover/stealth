"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import type { ColumnDef } from "@tanstack/react-table";
import { useState } from "react";
import { toast } from "sonner";
import {
  useDeleteWebhook,
  useRotateWebhookSecret,
  useUpdateWebhook,
} from "@/api/mutations";
import { nextCursor } from "@/api/pagination";
import { useWebhook, useWebhookDeliveries, useWebhooks } from "@/api/queries";
import type { WebhookDelivery } from "@/api/types";
import type { components } from "@/api/generated/schema";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { CopyButton } from "@/components/copy-button";
import { CreateDialog } from "@/components/create-dialog";
import { DataTable } from "@/components/data-table";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { OneTimeSecretDialog } from "@/components/one-time-secret-dialog";
import { PageHeader } from "@/components/page-header";
import { ResourceId } from "@/components/resource-id";
import { Badge, HttpStatusBadge, StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";
import { formatDate, formatRelative } from "@/lib/format";
import { pageControls } from "@/lib/pagination";
import {
  parseCommaSeparatedValues,
  webhookDeliveryStatusLabel,
} from "@/features/integrations/integration-values";
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
  const router = useRouter();
  const webhook = useWebhook(projectId, webhookId);
  const permissions = useWebhooks(projectId);
  const deliveriesNavigation = useCursorPagination("deliveries_cursor");
  const deliveries = useWebhookDeliveries(projectId, webhookId, {
    cursor: deliveriesNavigation.cursor,
  });
  const rotate = useRotateWebhookSecret(projectId, webhookId);
  const update = useUpdateWebhook(projectId, webhookId);
  const remove = useDeleteWebhook(projectId, webhookId);
  const [secret, setSecret] = useState<string | null>(null);
  const current = webhook.data?.webhook;
  const canManage = permissions.data?.can_manage === true;
  const base = `/organizations/${organizationId}/projects/${projectId}`;

  if (webhook.isPending) return <LoadingState rows={5} />;
  if (webhook.error && !current)
    return (
      <ErrorState
        title="Could not load webhook"
        error={webhook.error}
        retry={() => webhook.refetch()}
      />
    );
  if (!current)
    return (
      <EmptyState
        title="Webhook not found"
        description="The webhook may have been removed or is outside this project."
      />
    );

  const columns: ColumnDef<WebhookDelivery, unknown>[] = [
    {
      id: "delivery_id",
      header: "Delivery",
      cell: ({ row }) => (
        <ResourceId id={row.original.id} label="Delivery ID" />
      ),
    },
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
      cell: ({ row }) => (
        <StatusBadge status={webhookDeliveryStatusLabel(row.original.status)} />
      ),
    },
    {
      accessorKey: "last_status_code",
      header: "HTTP",
      cell: ({ row }) => (
        <HttpStatusBadge status={row.original.last_status_code} />
      ),
    },
    {
      accessorKey: "attempt_count",
      header: "Attempts",
      cell: ({ row }) => row.original.attempt_count,
    },
    {
      accessorKey: "created_at",
      header: "Created",
      cell: ({ row }) => (
        <span title={row.original.created_at}>
          {formatRelative(row.original.created_at)}
        </span>
      ),
    },
    {
      accessorKey: "last_error",
      header: "Error",
      cell: ({ row }) => (
        <span
          className="block max-w-xs truncate text-xs text-rose-200/80"
          title={row.original.last_error ?? undefined}
        >
          {row.original.last_error ?? "—"}
        </span>
      ),
    },
  ];

  const secretResult = (result: unknown) =>
    result as components["schemas"]["WebhookSecretResponse"] | undefined;

  const handleUpdate = async (values: Record<string, string>) => {
    await update.mutateAsync({
      name: values.name.trim(),
      url: values.url.trim(),
      events: parseCommaSeparatedValues(values.events),
      enabled: values.enabled === "true",
    });
    toast.success("Webhook settings updated");
  };

  const handleDelete = async () => {
    await remove.mutateAsync();
    toast.success("Webhook deleted");
    router.replace(`${base}/webhooks`);
  };

  return (
    <>
      <BackLink href={`${base}/webhooks`} label="Back to webhooks" />
      <PageHeader
        eyebrow="Webhook"
        title={current.name}
        description="Deliver platform events and inspect delivery history."
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <StatusBadge status={current.enabled ? "active" : "inactive"} />
            {canManage ? (
              <>
                <CreateDialog
                  triggerLabel="Edit settings"
                  submitLabel="Save changes"
                  pendingLabel="Saving changes…"
                  title="Edit webhook settings"
                  description="The Go API validates the HTTPS endpoint and event names before saving."
                  fields={[
                    {
                      name: "name",
                      label: "Name",
                      defaultValue: current.name,
                    },
                    {
                      name: "url",
                      label: "HTTPS URL",
                      type: "url",
                      defaultValue: current.url,
                    },
                    {
                      name: "events",
                      label: "Events",
                      required: false,
                      defaultValue: current.events.join(", "),
                      help: "Comma-separated event names, or * for all.",
                    },
                    {
                      name: "enabled",
                      label: "State",
                      type: "select",
                      defaultValue: current.enabled ? "true" : "false",
                      options: [
                        { value: "true", label: "Enabled" },
                        { value: "false", label: "Disabled" },
                      ],
                    },
                  ]}
                  pending={update.isPending}
                  onSubmit={handleUpdate}
                />
                <ConfirmDialog
                  trigger={
                    <Button variant="outline" disabled={rotate.isPending}>
                      Rotate secret
                    </Button>
                  }
                  title="Rotate this webhook secret?"
                  description="The current signing secret will stop working for future deliveries. Save the new secret before finishing; it will only be returned once."
                  confirmLabel="Rotate secret"
                  pending={rotate.isPending}
                  onConfirm={async () => {
                    const result = await rotate.mutateAsync();
                    const response = secretResult(result);
                    if (response?.secret) setSecret(response.secret);
                    rotate.reset();
                    toast.success("Webhook secret rotated");
                  }}
                />
                <ConfirmDialog
                  trigger={
                    <Button variant="destructive" disabled={remove.isPending}>
                      Delete webhook
                    </Button>
                  }
                  title="Delete webhook?"
                  description="This stops future deliveries to this endpoint."
                  confirmLabel="Delete webhook"
                  pending={remove.isPending}
                  onConfirm={handleDelete}
                />
              </>
            ) : permissions.data ? (
              <Badge variant="neutral">Read-only</Badge>
            ) : null}
          </div>
        }
      />
      <div className="mb-5 flex flex-wrap items-center gap-3 text-xs text-slate-500">
        <ResourceId id={current.id} label="Webhook ID" />
        <span>Created {formatDate(current.created_at)}</span>
        <span>Updated {formatDate(current.updated_at)}</span>
      </div>
      {webhook.error ? (
        <ErrorState
          title="Could not refresh webhook"
          error={webhook.error}
          retry={() => webhook.refetch()}
        />
      ) : null}
      {permissions.error ? (
        <ErrorState
          title="Could not load webhook permissions"
          error={permissions.error}
          retry={() => permissions.refetch()}
        />
      ) : null}
      <Card className="mb-5">
        <CardHeader>
          <CardTitle>Settings</CardTitle>
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
              <dd className="mt-1 font-mono text-xs text-slate-300">
                {current.events.join(", ") || "—"}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-slate-600">Secret state</dt>
              <dd className="mt-1 text-sm text-slate-300">
                Configured · secret withheld
              </dd>
            </div>
            <div>
              <dt className="text-xs text-slate-600">Delivery health</dt>
              <dd className="mt-1 text-sm text-slate-300">
                {current.failure_count} recorded failure
                {current.failure_count === 1 ? "" : "s"}
                {current.last_delivery_at
                  ? ` · last delivery ${formatDate(current.last_delivery_at)}`
                  : ""}
              </dd>
            </div>
          </dl>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Deliveries</CardTitle>
          <p className="mt-1 text-xs text-slate-500">
            HTTP status and delivery lifecycle are reported separately. The Go
            API exposes bounded metadata only; request/response bodies, headers,
            latency, and retry actions are not available in this contract.
          </p>
        </CardHeader>
        {deliveries.error ? (
          <CardContent>
            <ErrorState
              title="Could not load webhook deliveries"
              error={deliveries.error}
              retry={() => deliveries.refetch()}
            />
          </CardContent>
        ) : null}
        <DataTable
          data={deliveries.data?.deliveries ?? []}
          columns={columns}
          loading={deliveries.isPending}
          empty="No deliveries yet"
          serverPagination={pageControls(
            deliveriesNavigation,
            nextCursor(deliveries.data),
            deliveries.isFetching,
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
      <Button asChild variant="ghost" className="mt-5">
        <Link href={`${base}/webhooks`}>Return to webhooks</Link>
      </Button>
    </>
  );
}
