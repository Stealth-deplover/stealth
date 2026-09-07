import { Suspense } from "react";
import { PasswordRecoveryView } from "@/features/auth/auth-flow-views";
import { Skeleton } from "@/components/ui/skeleton";

export default function ResetPasswordPage() {
  return (
    <Suspense
      fallback={<Skeleton className="h-[28rem] w-full max-w-md rounded-2xl" />}
    >
      <PasswordRecoveryView />
    </Suspense>
  );
}
