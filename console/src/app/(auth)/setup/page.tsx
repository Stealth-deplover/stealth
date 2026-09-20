import { Suspense } from "react";
import { BrowserSetupView } from "@/features/auth/browser-setup-view";
import { LoadingState } from "@/components/feedback/loading-state";
import { Skeleton } from "@/components/ui/skeleton";

export default function SetupPage() {
  return (
    <Suspense
      fallback={
        <LoadingState
          label="Loading browser setup…"
          className="h-[36rem] w-full max-w-5xl"
        >
          <Skeleton className="h-full w-full rounded-2xl" />
        </LoadingState>
      }
    >
      <BrowserSetupView />
    </Suspense>
  );
}
