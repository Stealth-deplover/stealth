"use client";
import { Activity } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent } from "@/components/ui/card";

export function OrganizationIncidentsView({
  organizationId,
}: {
  organizationId: string;
}) {
  return (
    <>
      <PageHeader
        eyebrow="Organization"
        title="Incidents"
        description="Operational incident workflows are available from the backend; this surface stays explicit about the current incident data boundary."
      />
      <Card>
        <CardContent className="p-7">
          <div className="flex items-start gap-3">
            <Activity className="mt-0.5 size-5 text-amber-300" />
            <div>
              <h2 className="text-sm font-semibold text-white">
                Incident timeline
              </h2>
              <p className="mt-1 max-w-xl text-sm leading-6 text-slate-500">
                Use the organization incident endpoints for durable incidents
                and timeline updates. The project console keeps this view
                separate from runtime health metrics.
              </p>
              <code className="mt-4 block rounded-lg border border-stealth-border bg-black/20 px-3 py-2 font-mono text-xs text-slate-400">
                /v1/organizations/{organizationId}/incidents
              </code>
            </div>
          </div>
        </CardContent>
      </Card>
    </>
  );
}
