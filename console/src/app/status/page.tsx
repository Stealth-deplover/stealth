"use client";

import { usePublicStatusPage } from "@/api/queries";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { formatDate } from "@/lib/format";

export default function PublicStatusPage() {
  const statusPage = usePublicStatusPage();

  if (statusPage.isPending) {
    return (
      <main className="mx-auto min-h-screen max-w-4xl bg-void px-5 py-16 text-paper sm:px-8">
        <LoadingState rows={4} />
      </main>
    );
  }
  if (statusPage.error) {
    return (
      <main className="mx-auto flex min-h-screen max-w-4xl items-center bg-void px-5 py-16 sm:px-8">
        <ErrorState
          title="Status page unavailable"
          error={statusPage.error}
          retry={() => statusPage.refetch()}
        />
      </main>
    );
  }
  const data = statusPage.data;
  if (!data) return null;

  return (
    <main className="min-h-screen bg-void px-5 py-16 text-paper sm:px-8">
      <div className="mx-auto max-w-4xl">
        <header className="mb-10 border-b border-graphite pb-8">
          <p className="text-xs uppercase tracking-[0.14em] text-fog">
            Stealth status
          </p>
          <h1 className="mt-3 text-3xl tracking-[-0.02em]">{data.name}</h1>
          {data.description ? (
            <p className="mt-3 max-w-2xl text-sm leading-6 text-fog">
              {data.description}
            </p>
          ) : null}
        </header>
        <div className="space-y-4">
          {data.components.length ? (
            data.components.map((component) => (
              <Card key={component.name}>
                <CardContent className="flex flex-wrap items-center justify-between gap-4 p-5">
                  <div>
                    <h2 className="text-sm text-mist">{component.name}</h2>
                    {component.description ? (
                      <p className="mt-1 text-sm text-fog">
                        {component.description}
                      </p>
                    ) : null}
                  </div>
                  <Badge variant={statusVariant(component.status)}>
                    {statusLabel(component.status)}
                  </Badge>
                </CardContent>
              </Card>
            ))
          ) : (
            <Card>
              <CardContent className="p-8 text-center text-sm text-fog">
                No components have been published.
              </CardContent>
            </Card>
          )}
        </div>
        {data.incidents.length ? (
          <Card className="mt-8">
            <CardHeader>
              <CardTitle>Recent incidents</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              {data.incidents.map((incident) => (
                <article
                  key={incident.id}
                  className="border-b border-graphite pb-4 last:border-0 last:pb-0"
                >
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <h2 className="text-sm text-mist">{incident.title}</h2>
                    <Badge variant={statusVariant(incident.status)}>
                      {statusLabel(incident.status)}
                    </Badge>
                  </div>
                  <p className="mt-2 text-xs text-fog">
                    Started {formatDate(incident.started_at)}
                    {incident.resolved_at
                      ? ` · Resolved ${formatDate(incident.resolved_at)}`
                      : ""}
                  </p>
                </article>
              ))}
            </CardContent>
          </Card>
        ) : null}
        <p className="mt-8 text-xs text-fog">
          Updated {formatDate(data.updated_at)}
        </p>
      </div>
    </main>
  );
}

function statusVariant(
  status: string,
): "success" | "warning" | "error" | "neutral" {
  switch (status) {
    case "operational":
    case "resolved":
      return "success";
    case "degraded":
    case "partial_outage":
    case "monitoring":
    case "maintenance":
      return "warning";
    case "major_outage":
    case "investigating":
      return "error";
    default:
      return "neutral";
  }
}

function statusLabel(status: string) {
  return status
    .replace(/_/g, " ")
    .replace(/^\w/, (letter) => letter.toUpperCase());
}
