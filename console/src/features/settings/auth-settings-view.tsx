"use client";
import { Settings2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { useUpdateAuthSettings } from "@/api/mutations";
import { useAuthSettings } from "@/api/queries";
import { EmptyState } from "@/components/empty-state";
import { ErrorState } from "@/components/feedback/error-state";
import { LoadingState } from "@/components/feedback/loading-state";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

export function AuthSettingsView({ projectId }: { projectId: string }) {
  const query = useAuthSettings(projectId);
  const update = useUpdateAuthSettings(projectId);
  const [origins, setOrigins] = useState<string | null>(null);
  if (query.error)
    return <ErrorState error={query.error} retry={() => query.refetch()} />;
  if (query.isLoading) return <LoadingState rows={4} />;
  const settings = query.data?.settings;
  if (!settings)
    return (
      <EmptyState
        title="Auth settings unavailable"
        description="The API did not return project Auth settings."
      />
    );
  const originValue = origins ?? settings.cors_origins.join("\n");
  return (
    <>
      <PageHeader
        eyebrow="Project Auth"
        title="Auth settings"
        description="Configure registration and explicit browser CORS origins for the project application API."
      />
      <Card>
        <CardHeader>
          <CardTitle>Registration</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex items-center justify-between rounded-lg border border-stealth-border p-4">
            <div>
              <p className="text-sm font-medium text-white">
                Allow registrations
              </p>
              <p className="mt-1 text-xs text-slate-500">
                The project application API enforces this setting.
              </p>
            </div>
            <Button
              variant={settings.registration_enabled ? "default" : "outline"}
              onClick={() =>
                update.mutate(
                  { registration_enabled: !settings.registration_enabled },
                  { onSuccess: () => toast.success("Auth setting updated") },
                )
              }
            >
              {settings.registration_enabled ? "Enabled" : "Disabled"}
            </Button>
          </div>
        </CardContent>
      </Card>
      <Card className="mt-5">
        <CardHeader>
          <CardTitle>Browser CORS origins</CardTitle>
        </CardHeader>
        <CardContent>
          <Label htmlFor="cors">One explicit HTTP(S) origin per line</Label>
          <Textarea
            id="cors"
            className="mt-2"
            value={originValue}
            onChange={(event) => setOrigins(event.target.value)}
            placeholder="https://app.example.com"
          />
          <Button
            className="mt-3"
            disabled={update.isPending}
            onClick={() =>
              update.mutate(
                {
                  cors_origins: originValue
                    .split("\n")
                    .map((origin) => origin.trim())
                    .filter(Boolean),
                },
                { onSuccess: () => toast.success("Origins saved") },
              )
            }
          >
            <Settings2 className="size-4" /> Save origins
          </Button>
        </CardContent>
      </Card>
    </>
  );
}
