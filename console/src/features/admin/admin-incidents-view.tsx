"use client";

import { useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { AlertTriangle, Plus } from "lucide-react";
import {
  useAddAdminIncidentEvent,
  useCreateAdminIncident,
  useUpdateAdminIncident,
} from "@/api/mutations";
import { useAdminIncident, useAdminIncidents } from "@/api/queries";
import {
  CreateAdminIncidentRequestSeverity,
  CreateAdminIncidentRequestStatus,
  AddAdminIncidentEventRequestKind,
  UpdateAdminIncidentRequestStatus,
} from "@/api/generated/schema";
import { ErrorState, errorMessage } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { Badge, StatusBadge } from "@/components/ui/badge";
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
import { Textarea } from "@/components/ui/textarea";
import { formatDate } from "@/lib/format";
import { AdminShell } from "./admin-shell";

export function AdminIncidentsView() {
  const incidents = useAdminIncidents({ limit: 100 });
  const [dialogOpen, setDialogOpen] = useState(false);
  const [selectedId, setSelectedId] = useState<string>();
  return (
    <AdminShell>
      <PageHeader
        eyebrow="Admin / Incidents"
        title="Incidents"
        description="A durable timeline for outages, deployments, monitor changes, and owner notes. Incidents are separate from alert state."
        actions={
          <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
            <DialogTrigger asChild>
              <Button size="sm">
                <Plus className="size-3.5" aria-hidden="true" /> Open incident
              </Button>
            </DialogTrigger>
            <CreateIncidentDialog onCreated={() => setDialogOpen(false)} />
          </Dialog>
        }
      />
      {incidents.isPending ? <LoadingState rows={5} /> : null}
      {incidents.error ? (
        <ErrorState
          title="Could not load incidents"
          error={incidents.error}
          retry={() => incidents.refetch()}
        />
      ) : null}
      {incidents.data && !incidents.data.items.length ? (
        <EmptyIncidents onAdd={() => setDialogOpen(true)} />
      ) : null}
      {incidents.data?.items.length ? (
        <div className="overflow-hidden rounded-xl border border-graphite bg-carbon">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[820px] text-left text-sm">
              <thead className="border-b border-graphite bg-white/[0.02] text-xs uppercase tracking-[0.1em] text-fog">
                <tr>
                  <th className="px-4 py-3 font-medium">Incident</th>
                  <th className="px-4 py-3 font-medium">Severity</th>
                  <th className="px-4 py-3 font-medium">Status</th>
                  <th className="px-4 py-3 font-medium">Started</th>
                  <th className="px-4 py-3 font-medium">Services</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-graphite">
                {incidents.data.items.map((incident) => (
                  <tr
                    key={incident.id}
                    tabIndex={0}
                    className="cursor-pointer hover:bg-white/[0.025] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-acid-lime/70"
                    onClick={() => setSelectedId(incident.id)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter" || event.key === " ") {
                        event.preventDefault();
                        setSelectedId(incident.id);
                      }
                    }}
                  >
                    <td className="px-4 py-4">
                      <p className="text-mist">{incident.title}</p>
                      <p className="mt-1 font-mono text-[11px] text-fog">
                        {incident.id}
                      </p>
                    </td>
                    <td className="px-4 py-4">
                      <Badge
                        variant={
                          incident.severity === "critical"
                            ? "error"
                            : incident.severity === "warning"
                              ? "warning"
                              : "neutral"
                        }
                      >
                        {incident.severity}
                      </Badge>
                    </td>
                    <td className="px-4 py-4">
                      <StatusBadge status={incident.status} />
                    </td>
                    <td className="px-4 py-4 text-xs text-fog">
                      {formatDate(incident.started_at)}
                    </td>
                    <td className="px-4 py-4 text-xs text-fog">
                      {incident.services.join(" · ")}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ) : null}

      <IncidentDetail
        incidentId={selectedId}
        onClose={() => setSelectedId(undefined)}
      />
    </AdminShell>
  );
}

function CreateIncidentDialog({ onCreated }: { onCreated: () => void }) {
  const mutation = useCreateAdminIncident();
  const [title, setTitle] = useState("");
  const [severity, setSeverity] = useState<CreateAdminIncidentRequestSeverity>(
    CreateAdminIncidentRequestSeverity.warning,
  );
  const [services, setServices] = useState("API");
  const [message, setMessage] = useState("");
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    mutation.mutate(
      {
        title,
        severity,
        status: CreateAdminIncidentRequestStatus.investigating,
        services: services
          .split(",")
          .map((value) => value.trim())
          .filter(Boolean),
        message,
      },
      { onSuccess: onCreated },
    );
  }
  return (
    <DialogContent>
      <DialogHeader>
        <DialogTitle>Open incident</DialogTitle>
        <DialogDescription>
          Record the affected services and the first timeline note. Further
          updates remain append-only events.
        </DialogDescription>
      </DialogHeader>
      <form className="space-y-4" onSubmit={submit}>
        <Field label="Title" htmlFor="incident-title">
          <Input
            id="incident-title"
            required
            value={title}
            onChange={(event) => setTitle(event.target.value)}
            placeholder="API responses degraded"
          />
        </Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Severity" htmlFor="incident-severity">
            <select
              id="incident-severity"
              value={severity}
              onChange={(event) =>
                setSeverity(
                  event.target.value as CreateAdminIncidentRequestSeverity,
                )
              }
              className="min-h-11 w-full rounded-md border border-graphite bg-carbon px-3.5 text-sm text-mist outline-none focus:border-acid-lime/70 focus:ring-2 focus:ring-acid-lime/15"
            >
              <option value="info">Info</option>
              <option value="warning">Warning</option>
              <option value="critical">Critical</option>
            </select>
          </Field>
          <Field
            label="Services"
            htmlFor="incident-services"
            hint="Comma-separated component names."
          >
            <Input
              id="incident-services"
              required
              value={services}
              onChange={(event) => setServices(event.target.value)}
            />
          </Field>
        </div>
        <Field label="Opening note" htmlFor="incident-message">
          <Textarea
            id="incident-message"
            value={message}
            onChange={(event) => setMessage(event.target.value)}
            placeholder="What is known so far?"
          />
        </Field>
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
            {mutation.isPending ? "Opening…" : "Open incident"}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  );
}

function IncidentDetail({
  incidentId,
  onClose,
}: {
  incidentId: string | undefined;
  onClose: () => void;
}) {
  const detail = useAdminIncident(incidentId);
  const update = useUpdateAdminIncident(incidentId ?? "");
  const addEvent = useAddAdminIncidentEvent(incidentId ?? "");
  const [note, setNote] = useState("");
  if (!incidentId) return null;
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{detail.data?.incident.title ?? "Incident"}</DialogTitle>
          <DialogDescription>
            Timeline events are retained in the control plane and do not expose
            telemetry payloads.
          </DialogDescription>
        </DialogHeader>
        {detail.isPending ? <LoadingState rows={3} /> : null}
        {detail.error ? (
          <ErrorState error={detail.error} retry={() => detail.refetch()} />
        ) : null}
        {detail.data ? (
          <div className="space-y-5">
            <div className="flex flex-wrap items-center gap-3">
              <StatusBadge status={detail.data.incident.status} />
              <Badge
                variant={
                  detail.data.incident.severity === "critical"
                    ? "error"
                    : detail.data.incident.severity === "warning"
                      ? "warning"
                      : "neutral"
                }
              >
                {detail.data.incident.severity}
              </Badge>
              <span className="text-xs text-fog">
                Started {formatDate(detail.data.incident.started_at)}
              </span>
            </div>
            <div className="flex flex-wrap gap-2">
              {["investigating", "identified", "monitoring", "resolved"].map(
                (status) => (
                  <Button
                    key={status}
                    type="button"
                    size="sm"
                    variant={
                      detail.data?.incident.status === status
                        ? "default"
                        : "secondary"
                    }
                    disabled={
                      update.isPending ||
                      status === detail.data?.incident.status
                    }
                    onClick={() =>
                      update.mutate({
                        status: status as UpdateAdminIncidentRequestStatus,
                      })
                    }
                  >
                    {status}
                  </Button>
                ),
              )}
            </div>
            <div className="space-y-2 border-l border-graphite pl-4">
              {detail.data.incident.events.map((event) => (
                <div key={event.id} className="relative">
                  <span className="absolute -left-[21px] top-1.5 size-2 rounded-full bg-smoke" />
                  <p className="text-sm text-mist">{event.message}</p>
                  <p className="mt-1 font-mono text-[11px] text-fog">
                    {event.kind} · {formatDate(event.created_at)}
                  </p>
                </div>
              ))}
            </div>
            <form
              className="space-y-2 border-t border-graphite pt-4"
              onSubmit={(event) => {
                event.preventDefault();
                if (!note.trim()) return;
                addEvent.mutate(
                  {
                    kind: AddAdminIncidentEventRequestKind.note,
                    message: note,
                  },
                  { onSuccess: () => setNote("") },
                );
              }}
            >
              <Label htmlFor="incident-note">Add note</Label>
              <Textarea
                id="incident-note"
                value={note}
                onChange={(event) => setNote(event.target.value)}
                placeholder="Add a timeline observation"
              />
              <div className="flex items-center justify-between gap-3">
                {addEvent.error || update.error ? (
                  <p className="text-xs text-coral-red" role="alert">
                    {errorMessage(addEvent.error ?? update.error)}
                  </p>
                ) : (
                  <span />
                )}
                <Button
                  type="submit"
                  size="sm"
                  disabled={addEvent.isPending || !note.trim()}
                >
                  Add note
                </Button>
              </div>
            </form>
          </div>
        ) : null}
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="secondary">Close</Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Field({
  label,
  htmlFor,
  hint,
  children,
}: {
  label: string;
  htmlFor: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
      {hint ? <p className="text-[11px] text-fog">{hint}</p> : null}
    </div>
  );
}

function EmptyIncidents({ onAdd }: { onAdd: () => void }) {
  return (
    <div className="rounded-xl border border-dashed border-graphite bg-carbon/50 p-10 text-center">
      <AlertTriangle className="mx-auto size-5 text-fog" aria-hidden="true" />
      <h2 className="mt-4 text-sm text-paper">No incidents recorded</h2>
      <p className="mt-2 text-sm text-fog">
        Open an incident when an alert needs a human timeline.
      </p>
      <Button className="mt-5" size="sm" onClick={onAdd}>
        <Plus className="size-3.5" aria-hidden="true" /> Open incident
      </Button>
    </div>
  );
}
