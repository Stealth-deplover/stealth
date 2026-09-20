import { Suspense } from "react";
import { PasswordRecoveryView } from "@/features/auth/auth-flow-views";
import { LoadingState } from "@/components/feedback/loading-state";
import { Skeleton } from "@/components/ui/skeleton";

export default function ResetPasswordPage() {
  return (
    <Suspense
      fallback={
        <LoadingState
          label="Loading password reset form…"
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
