"use client";

import { AlertTriangle, RefreshCw } from "lucide-react";
import { ApiError } from "@/api/client";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";

export function errorMessage(error: unknown) {
  if (error instanceof ApiError) {
    if (error.status === 401) return "Your session has expired. Sign in again to continue.";
    if (error.status === 403) return "You do not have permission to view this resource.";
    if (error.status === 404) return "This resource could not be found.";
    if (error.status === 429) return "The API is rate limiting requests. Try again shortly.";
    return error.message;
  }
  return error instanceof Error ? error.message : "Something unexpected happened.";
}

export function ErrorState({ error, retry }: { error: unknown; retry?: () => void }) {
  return <Card className="border-rose-300/20 bg-rose-400/[0.04]">
    <CardHeader><CardTitle className="flex items-center gap-2 text-rose-200"><AlertTriangle className="size-4" /> Unable to load this view</CardTitle></CardHeader>
    <CardContent className="flex flex-wrap items-center gap-3">
      <p className="text-sm text-slate-400">{errorMessage(error)}</p>
      {retry ? <Button variant="outline" size="sm" onClick={retry}><RefreshCw className="size-3.5" /> Retry</Button> : null}
    </CardContent>
  </Card>;
}
