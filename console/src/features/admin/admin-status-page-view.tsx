"use client";

import { useState } from "react";
import type { ReactNode } from "react";
import { ExternalLink, Globe2, Plus, Save, Trash2 } from "lucide-react";
import { useAdminStatusPage, useAdminIncidents } from "@/api/queries";
import { useUpdateAdminStatusPage } from "@/api/mutations";
import type { components } from "@/api/generated/schema";
import { AdminStatusPageComponentStatus } from "@/api/generated/schema";
import { ErrorState, errorMessage } from "@/components/feedback/error-state";
import { InlineError } from "@/components/feedback/inline-error";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { formatDate } from "@/lib/format";
import { AdminShell } from "./admin-shell";

type StatusRequest = components["schemas"]["UpdateAdminStatusPageRequest"];
type StatusComponent = components["schemas"]["AdminStatusPageComponent"];
type StatusForm = Omit<StatusRequest, "components"> & {
  components: StatusComponent[];
};

const defaultComponent = (): StatusComponent => ({
  name: "API",
  status: AdminStatusPageComponentStatus.operational,
  description: "",
});

export function AdminStatusPageView() {
  const statusPage = useAdminStatusPage();
  const incidents = useAdminIncidents({ limit: 100 });
  const update = useUpdateAdminStatusPage();
  const [draft, setDraft] = useState<StatusForm | null>(null);
  const serverForm: StatusForm | undefined = statusPage.data
    ? {
        name: statusPage.data.name,
        description: statusPage.data.description,
        is_public: statusPage.data.is_public,
        components: statusPage.data.components,
        published_incidents: statusPage.data.published_incidents,
      }
    : undefined;
  const form = draft ?? serverForm;

  if (statusPage.error)
    return (
      <AdminShell>
        <ErrorState
          title="Could not load status page"
          error={statusPage.error}
          retry={() => statusPage.refetch()}
        />
      </AdminShell>
    );
  if (statusPage.isPending || !form)
    return (
      <AdminShell>
        <LoadingState rows={5} />
      </AdminShell>
    );

  const componentsValue = form.components ?? [];
  const setForm = (next: StatusForm) => setDraft(next);
  const setComponent = (index: number, patch: Partial<StatusComponent>) => {
    setDraft({
      ...form,
      components: componentsValue.map((component, itemIndex) =>
        itemIndex === index ? { ...component, ...patch } : component,
      ),
    });
  };
  const save = () => update.mutate(form);

  return (
    <AdminShell>
      <PageHeader
        eyebrow="Admin / Status page"
        title="Status page"
        description="Publish a minimal external status view without exposing internal telemetry, logs, or audit history."
        actions={
          <div className="flex items-center gap-3">
            {form.is_public ? (
              <a
                href="/status"
                target="_blank"
                rel="noreferrer"
                className="inline-flex items-center gap-2 text-xs text-mist hover:text-paper"
              >
                Open public status{" "}
                <ExternalLink className="size-3.5" aria-hidden="true" />
              </a>
            ) : null}
            <Button size="sm" onClick={save} disabled={update.isPending}>
              <Save className="size-3.5" aria-hidden="true" />{" "}
              {update.isPending ? "Saving…" : "Save changes"}
            </Button>
          </div>
        }
      />
      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_360px]">
        <Card>
          <CardHeader>
            <CardTitle>Public configuration</CardTitle>
          </CardHeader>
          <CardContent className="space-y-5">
            <Field label="Page name" htmlFor="status-name">
              <Input
                id="status-name"
                value={form.name}
                onChange={(event) =>
                  setForm({ ...form, name: event.target.value })
                }
              />
            </Field>
            <Field label="Description" htmlFor="status-description">
              <Textarea
                id="status-description"
                value={form.description ?? ""}
                onChange={(event) =>
                  setForm({ ...form, description: event.target.value })
                }
              />
            </Field>
            <label className="flex items-start gap-3 rounded-md border border-graphite bg-void/40 p-3">
              <input
                type="checkbox"
                checked={form.is_public}
                onChange={(event) =>
                  setForm({ ...form, is_public: event.target.checked })
                }
                className="mt-0.5 accent-acid-lime"
              />
              <span>
                <span className="block text-sm text-mist">
                  Publish status page
                </span>
                <span className="mt-1 block text-xs leading-5 text-fog">
                  Only the selected component labels and published incident
                  summaries become public.
                </span>
              </span>
            </label>
            <div className="space-y-3">
              <div className="flex items-center justify-between">
                <div>
                  <h3 className="text-sm text-paper">Components</h3>
                  <p className="mt-1 text-xs text-fog">
                    Use operational, degraded, partial_outage, major_outage, or
                    maintenance.
                  </p>
                </div>
                <Button
                  type="button"
                  size="sm"
                  variant="secondary"
                  onClick={() =>
                    setForm({
                      ...form,
                      components: [...componentsValue, defaultComponent()],
                    })
                  }
                >
                  <Plus className="size-3.5" aria-hidden="true" /> Add
                </Button>
              </div>
              {componentsValue.map((component, index) => (
                <div
                  key={`${component.name}-${index}`}
                  className="grid gap-3 rounded-md border border-graphite bg-void/40 p-3 sm:grid-cols-[1fr_170px_auto]"
                >
                  <Input
                    aria-label={`Component ${index + 1} name`}
                    value={component.name}
                    onChange={(event) =>
                      setComponent(index, { name: event.target.value })
                    }
                  />
                  <select
                    aria-label={`Component ${index + 1} status`}
                    value={component.status ?? "operational"}
                    onChange={(event) =>
                      setComponent(index, {
                        status: event.target
                          .value as AdminStatusPageComponentStatus,
                      })
                    }
                    className="min-h-11 rounded-md border border-graphite bg-carbon px-3 text-sm text-mist outline-none focus:border-acid-lime/70"
                  >
                    <option value="operational">Operational</option>
                    <option value="degraded">Degraded</option>
                    <option value="partial_outage">Partial outage</option>
                    <option value="major_outage">Major outage</option>
                    <option value="maintenance">Maintenance</option>
                  </select>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    aria-label={`Remove ${component.name}`}
                    onClick={() =>
                      setForm({
                        ...form,
                        components: componentsValue.filter(
                          (_, itemIndex) => itemIndex !== index,
                        ),
                      })
                    }
                  >
                    <Trash2 className="size-4 text-fog" aria-hidden="true" />
                  </Button>
                  <Input
                    className="sm:col-span-2"
                    aria-label={`Component ${index + 1} description`}
                    value={component.description ?? ""}
                    onChange={(event) =>
                      setComponent(index, { description: event.target.value })
                    }
                    placeholder="Optional public description"
                  />
                </div>
              ))}
            </div>
            {update.error ? (
              <p className="text-sm text-coral-red" role="alert">
                {errorMessage(update.error)}
              </p>
            ) : null}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Published incidents</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {incidents.error ? (
              <InlineError>
                Incident list is unavailable. Refresh this page to retry.
              </InlineError>
            ) : null}
            {incidents.data?.items.length
              ? incidents.data.items.map((incident) => {
                  const checked = form.published_incidents.includes(
                    incident.id,
                  );
                  return (
                    <label
                      key={incident.id}
                      className="flex items-start gap-3 rounded-md border border-graphite p-3"
                    >
                      <input
                        type="checkbox"
                        checked={checked}
                        onChange={(event) =>
                          setForm({
                            ...form,
                            published_incidents: event.target.checked
                              ? [...form.published_incidents, incident.id]
                              : form.published_incidents.filter(
                                  (id) => id !== incident.id,
                                ),
                          })
                        }
                        className="mt-0.5 accent-acid-lime"
                      />
                      <span className="min-w-0">
                        <span className="block truncate text-sm text-mist">
                          {incident.title}
                        </span>
                        <span className="mt-1 flex flex-wrap items-center gap-2">
                          <StatusBadge status={incident.status} />
                          <Badge variant="neutral">
                            {formatDate(incident.started_at)}
                          </Badge>
                        </span>
                      </span>
                    </label>
                  );
                })
              : null}
            {incidents.data && !incidents.data.items.length ? (
              <p className="text-sm leading-6 text-fog">
                No incidents available to publish.
              </p>
            ) : null}
          </CardContent>
        </Card>
      </div>
      <div className="mt-4 flex items-center gap-2 text-xs text-fog">
        <Globe2 className="size-3.5" aria-hidden="true" /> Public status API:{" "}
        <code className="font-mono text-mist">/v1/status-page</code>
      </div>
    </AdminShell>
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
    <div className="space-y-1.5">
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
    </div>
  );
}
