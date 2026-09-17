"use client";

import { useState } from "react";
import { useAdminAuditEvents } from "@/api/queries";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
import { AdminShell } from "./admin-shell";

export function AdminAuditView() {
  const [before, setBefore] = useState<string>();
  const audit = useAdminAuditEvents({ before, limit: 100 });

  return (
    <AdminShell>
      <PageHeader
        eyebrow="Admin / Audit & security"
        title="Instance activity"
        description="Owner and system actions from the durable audit ledger. Organization-scoped activity remains in each organization audit view."
      />
      {audit.isPending ? <LoadingState rows={6} /> : null}
      {audit.error ? (
        <ErrorState
          title="Could not load instance audit"
          error={audit.error}
          retry={() => audit.refetch()}
        />
      ) : null}
      {audit.data && !audit.data.items.length ? (
        <Card>
          <CardContent className="p-8 text-center text-sm text-fog">
            No instance-level audit events have been recorded.
          </CardContent>
        </Card>
      ) : null}
      {audit.data?.items.length ? (
        <Card className="overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[860px] text-left text-sm">
              <thead className="border-b border-graphite bg-white/[0.02] text-xs uppercase tracking-[0.1em] text-fog">
                <tr>
                  <th className="px-4 py-3 font-medium">Time</th>
                  <th className="px-4 py-3 font-medium">Action</th>
                  <th className="px-4 py-3 font-medium">Actor</th>
                  <th className="px-4 py-3 font-medium">Target</th>
                  <th className="px-4 py-3 font-medium">Metadata</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-graphite">
                {audit.data.items.map((event) => (
                  <tr
                    key={event.id}
                    className="align-top hover:bg-white/[0.025]"
                  >
                    <td className="whitespace-nowrap px-4 py-3 font-mono text-xs text-fog">
                      {formatDate(event.created_at)}
                    </td>
                    <td className="px-4 py-3 font-mono text-xs text-mist">
                      {event.action}
                    </td>
                    <td className="px-4 py-3 text-mist">
                      {event.actor_email ?? "system"}
                    </td>
                    <td className="px-4 py-3">
                      <p className="text-mist">{event.target_type}</p>
                      <p className="mt-1 font-mono text-[11px] text-fog">
                        {event.target_id ?? "—"}
                      </p>
                    </td>
                    <td className="max-w-[360px] whitespace-pre-wrap break-words px-4 py-3 font-mono text-[11px] text-fog">
                      {JSON.stringify(event.metadata)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {audit.data.next_cursor ? (
            <div className="border-t border-graphite p-4">
              <button
                type="button"
                className="text-xs text-mist underline-offset-4 hover:text-paper hover:underline"
                onClick={() => setBefore(audit.data?.next_cursor)}
              >
                Load older activity
              </button>
            </div>
          ) : null}
        </Card>
      ) : null}
    </AdminShell>
  );
}
