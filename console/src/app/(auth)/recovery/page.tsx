import { Suspense } from "react";
import { PasswordRecoveryView } from "@/features/auth/auth-flow-views";
import { LoadingState } from "@/components/feedback/loading-state";
import { Skeleton } from "@/components/ui/skeleton";

export default function RecoveryPage() {
  return (
    <Suspense
      fallback={
        <LoadingState
          label="Loading recovery form…"
          className="h-[28rem] w-full max-w-md"
        >
          <Skeleton className="h-full w-full rounded-2xl" />
        </LoadingState>
      }
    >
      <PasswordRecoveryView />
    </Suspense>
  );
}
