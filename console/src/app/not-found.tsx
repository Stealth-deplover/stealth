import Link from "next/link";
import { ArrowLeft, Compass } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";

export default function NotFound() {
  return (
    <main className="flex min-h-screen items-center justify-center bg-void px-6 py-12">
      <Card className="w-full max-w-lg">
        <CardContent className="p-8 text-center">
          <div className="mx-auto flex size-10 items-center justify-center rounded-md border border-graphite bg-white/[0.04] text-fog">
            <Compass className="size-5" aria-hidden="true" />
          </div>
          <p className="mt-5 text-[11px] font-medium uppercase tracking-[0.12em] text-fog">
            404 · Not found
          </p>
          <h1 className="mt-2 text-2xl font-semibold tracking-[-0.022em] text-paper">
            This resource is not available
          </h1>
          <p className="mx-auto mt-3 max-w-sm text-sm leading-6 text-fog">
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
