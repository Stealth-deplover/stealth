import { Suspense } from "react";
import { InvitationAcceptanceView } from "@/features/auth/invitation-acceptance-view";
import { Skeleton } from "@/components/ui/skeleton";

export default function AcceptInvitationPage() {
  return (
    <Suspense
      fallback={<Skeleton className="h-[24rem] w-full max-w-md rounded-2xl" />}
    >
      <InvitationAcceptanceView />
    </Suspense>
  );
}
