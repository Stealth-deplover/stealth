import { Suspense } from "react";
import { AccountVerificationView } from "@/features/auth/auth-flow-views";
import { LoadingState } from "@/components/feedback/loading-state";
import { Skeleton } from "@/components/ui/skeleton";

export default function VerifyPage() {
  return (
    <Suspense
      fallback={
        <LoadingState
          label="Loading verification…"
          className="h-[20rem] w-full max-w-md"
        >
          <Skeleton className="h-full w-full rounded-2xl" />
        </LoadingState>
      }
    >
      <AccountVerificationView />
    </Suspense>
  );
}
