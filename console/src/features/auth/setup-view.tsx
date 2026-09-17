"use client";

import Link from "next/link";
import { ArrowRight, CheckCircle2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { AuthCard } from "@/features/auth/auth-flow-views";

// Kept as a compatibility component for consumers that imported the former
// single-card setup view. The routed /setup page owns the complete browser
// wizard; this component deliberately has no Device Flow controls.
export function SetupView() {
  return (
    <AuthCard
      title="Open browser setup"
      description="Use the full Stealth setup wizard to create the GitHub App, authorize the first owner in a browser, and configure production ingress."
    >
      <CheckCircle2 className="size-6 text-emerald-300" />
      <p className="mt-4 text-sm leading-6 text-slate-400">
        GitHub Device Flow is not required for the browser setup path.
      </p>
      <Button asChild className="mt-6 w-full">
        <Link href="/setup">
          Open browser setup <ArrowRight className="size-4" />
        </Link>
      </Button>
    </AuthCard>
  );
}
