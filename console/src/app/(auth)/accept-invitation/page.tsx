import { Suspense } from "react";
import { InvitationAcceptanceView } from "@/features/auth/invitation-acceptance-view";
import { LoadingState } from "@/components/feedback/loading-state";
import { Skeleton } from "@/components/ui/skeleton";

export default function AcceptInvitationPage() {
  return (
    <Suspense
      fallback={
        <LoadingState
          label="Loading invitation…"
          className="h-[24rem] w-full max-w-md"
        >
          <Skeleton className="h-full w-full rounded-2xl" />
        </LoadingState>
      }
    >
      <InvitationAcceptanceView />
    </Suspense>
  );
}
