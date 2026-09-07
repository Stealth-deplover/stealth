import Link from "next/link";
import { ArrowLeft, Compass } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";

export default function NotFound() {
  return (
    <main className="flex min-h-screen items-center justify-center bg-stealth-bg px-6 py-12">
      <Card className="w-full max-w-lg">
        <CardContent className="p-8 text-center">
          <div className="mx-auto flex size-11 items-center justify-center rounded-xl border border-cyan-300/20 bg-cyan-300/10 text-cyan-200">
            <Compass className="size-5" />
          </div>
          <p className="mt-5 text-[11px] font-medium uppercase tracking-[0.18em] text-cyan-300/80">
            404 · Not found
          </p>
          <h1 className="mt-2 text-2xl font-semibold tracking-tight text-white">
            This resource is not available
          </h1>
          <p className="mx-auto mt-3 max-w-sm text-sm leading-6 text-stealth-muted">
            The route may be outdated, or the resource may have been removed
            from the current project.
          </p>
          <Button asChild className="mt-6">
            <Link href="/organizations">
              <ArrowLeft className="size-4" /> Return to organizations
            </Link>
          </Button>
        </CardContent>
      </Card>
    </main>
  );
}
