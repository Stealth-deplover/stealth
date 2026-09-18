"use client";

import { useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { BellRing, Plus, Trash2 } from "lucide-react";
import {
  useCreateAdminNotificationChannel,
  useDeleteAdminNotificationChannel,
} from "@/api/mutations";
import { useAdminNotificationChannels } from "@/api/queries";
import {
  CreateAdminNotificationChannelRequestKind,
  type components,
} from "@/api/generated/schema";
import { ErrorState, errorMessage } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { formatDate } from "@/lib/format";
import { AdminShell } from "./admin-shell";

type ChannelRequest =
  components["schemas"]["CreateAdminNotificationChannelRequest"];

export function AdminNotificationsView() {
  const channels = useAdminNotificationChannels({ limit: 100 });
  const remove = useDeleteAdminNotificationChannel();
  const [dialogOpen, setDialogOpen] = useState(false);

  return (
    <AdminShell>
      <PageHeader
        eyebrow="Admin / Notifications"
        title="Notification channels"
        description="Alert deliveries are queued durably and sent by the trusted worker. Secrets are encrypted and never returned to the browser."
        actions={
          <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
            <DialogTrigger asChild>
              <Button size="sm">
                <Plus className="size-3.5" aria-hidden="true" /> Add channel
              </Button>
            </DialogTrigger>
            <CreateChannelDialog onCreated={() => setDialogOpen(false)} />
          </Dialog>
        }
      />
      {channels.isPending ? <LoadingState rows={4} /> : null}
      {channels.error ? (
        <ErrorState
          title="Could not load notification channels"
          error={channels.error}
          retry={() => channels.refetch()}
        />
      ) : null}
      {channels.data && !channels.data.items.length ? (
        <div className="rounded-xl border border-dashed border-graphite bg-carbon/50 p-10 text-center">
          <BellRing className="mx-auto size-5 text-fog" aria-hidden="true" />
          <h2 className="mt-4 text-sm text-paper">No channels configured</h2>
          <p className="mx-auto mt-2 max-w-md text-sm leading-6 text-fog">
            Add a channel to receive firing and recovered alert transitions.
          </p>
        </div>
      ) : null}
      {channels.data?.items.length ? (
        <div className="overflow-hidden rounded-xl border border-graphite bg-carbon">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[760px] text-left text-sm">
              <thead className="border-b border-graphite bg-white/[0.02] text-xs uppercase tracking-[0.1em] text-fog">
                <tr>
                  <th className="px-4 py-3 font-medium">Channel</th>
                  <th className="px-4 py-3 font-medium">State</th>
                  <th className="px-4 py-3 font-medium">Last delivery</th>
                  <th className="px-4 py-3 font-medium">Updated</th>
                  <th className="px-4 py-3 font-medium" aria-label="Actions" />
                </tr>
              </thead>
              <tbody className="divide-y divide-graphite">
                {channels.data.items.map((channel) => (
                  <tr key={channel.id} className="hover:bg-white/[0.025]">
                    <td className="px-4 py-4">
                      <p className="text-mist">{channel.name}</p>
                      <p className="mt-1 font-mono text-[11px] text-fog">
                        {channel.kind}
                      </p>
                    </td>
                    <td className="px-4 py-4">
                      <div className="flex flex-wrap gap-2">
                        <Badge
                          variant={channel.enabled ? "success" : "neutral"}
                        >
                          {channel.enabled ? "enabled" : "paused"}
                        </Badge>
                        {channel.secret_configured ? (
                          <Badge variant="neutral">secret configured</Badge>
                        ) : null}
                        {channel.last_delivery_status ? (
                          <Badge
                            variant={
                              channel.last_delivery_status === "success"
                                ? "success"
                                : "error"
                            }
                          >
                            {channel.last_delivery_status}
                          </Badge>
                        ) : null}
                      </div>
                      {channel.last_error ? (
                        <p className="mt-2 max-w-sm text-xs text-coral-red">
                          {channel.last_error}
                        </p>
                      ) : null}
                    </td>
                    <td className="px-4 py-4 text-xs text-fog">
                      {formatDate(channel.last_delivery_at)}
                    </td>
                    <td className="px-4 py-4 text-xs text-fog">
                      {formatDate(channel.updated_at)}
                    </td>
                    <td className="px-4 py-4 text-right">
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        aria-label={`Delete ${channel.name}`}
                        disabled={remove.isPending}
                        onClick={() => {
                          if (
                            window.confirm(
                              `Delete the channel "${channel.name}"?`,
                            )
                          ) {
                            remove.mutate(channel.id);
                          }
                        }}
                      >
                        <Trash2
                          className="size-4 text-fog"
                          aria-hidden="true"
                        />
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {remove.error ? (
            <p
              className="border-t border-graphite p-4 text-sm text-coral-red"
              role="alert"
            >
              {errorMessage(remove.error)}
            </p>
          ) : null}
        </div>
      ) : null}
    </AdminShell>
  );
}

function CreateChannelDialog({ onCreated }: { onCreated: () => void }) {
  const mutation = useCreateAdminNotificationChannel();
  const [name, setName] = useState("");
  const [kind, setKind] = useState<ChannelRequest["kind"]>(
    CreateAdminNotificationChannelRequestKind.webhook,
  );
  const [endpoint, setEndpoint] = useState("");
  const [recipient, setRecipient] = useState("");
  const [botToken, setBotToken] = useState("");
  const [chatID, setChatID] = useState("");

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const config =
      kind === CreateAdminNotificationChannelRequestKind.email
        ? { recipient }
        : kind === CreateAdminNotificationChannelRequestKind.telegram
          ? { bot_token: botToken, chat_id: chatID }
          : { url: endpoint };
    mutation.mutate(
      { name, kind, enabled: true, config },
      { onSuccess: onCreated },
    );
  }

  return (
    <DialogContent>
      <DialogHeader>
        <DialogTitle>Add notification channel</DialogTitle>
        <DialogDescription>
          Provider credentials are encrypted at rest and used only by the
          worker.
        </DialogDescription>
      </DialogHeader>
      <form className="space-y-4" onSubmit={submit}>
        <Field label="Name" htmlFor="notification-name">
          <Input
            id="notification-name"
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="On-call webhook"
            required
          />
        </Field>
        <Field label="Provider" htmlFor="notification-kind">
          <select
            id="notification-kind"
            value={kind}
            onChange={(event) => {
              setKind(event.target.value as ChannelRequest["kind"]);
              setEndpoint("");
              setRecipient("");
              setBotToken("");
              setChatID("");
            }}
            className="min-h-11 w-full rounded-md border border-graphite bg-carbon px-3.5 text-sm text-mist outline-none focus:border-acid-lime/70 focus:ring-2 focus:ring-acid-lime/15"
          >
            <option value="webhook">Generic webhook</option>
            <option value="slack">Slack webhook</option>
            <option value="discord">Discord webhook</option>
            <option value="email">Email</option>
            <option value="telegram">Telegram</option>
          </select>
        </Field>
        {kind === CreateAdminNotificationChannelRequestKind.email ? (
          <Field label="Recipient" htmlFor="notification-recipient">
            <Input
              id="notification-recipient"
              type="email"
              value={recipient}
              onChange={(event) => setRecipient(event.target.value)}
              placeholder="owner@example.com"
              required
            />
          </Field>
        ) : null}
        {kind === CreateAdminNotificationChannelRequestKind.telegram ? (
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Bot token" htmlFor="notification-token">
              <Input
                id="notification-token"
                type="password"
                value={botToken}
                onChange={(event) => setBotToken(event.target.value)}
                autoComplete="new-password"
                required
              />
            </Field>
            <Field label="Chat ID" htmlFor="notification-chat-id">
              <Input
                id="notification-chat-id"
                value={chatID}
                onChange={(event) => setChatID(event.target.value)}
                required
              />
            </Field>
          </div>
        ) : null}
        {kind !== CreateAdminNotificationChannelRequestKind.email &&
        kind !== CreateAdminNotificationChannelRequestKind.telegram ? (
          <Field label="HTTPS webhook URL" htmlFor="notification-endpoint">
            <Input
              id="notification-endpoint"
              type="url"
              value={endpoint}
              onChange={(event) => setEndpoint(event.target.value)}
              placeholder="https://hooks.example.test/..."
              required
            />
          </Field>
        ) : null}
        {mutation.error ? (
          <p className="text-sm text-coral-red" role="alert">
            {errorMessage(mutation.error)}
          </p>
        ) : null}
        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="secondary">
              Cancel
            </Button>
          </DialogClose>
          <Button type="submit" disabled={mutation.isPending}>
            {mutation.isPending ? "Saving…" : "Save channel"}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  );
}

function Field({
  label,
  htmlFor,
  children,
}: {
  label: string;
  htmlFor: string;
  children: ReactNode;
}) {
  return (
    <div className="space-y-2">
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
    </div>
  );
}
