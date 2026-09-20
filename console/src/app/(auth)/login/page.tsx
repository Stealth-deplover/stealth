import { Suspense } from "react";
import { LoginView } from "@/features/auth/login-view";
import { LoadingState } from "@/components/feedback/loading-state";
import { Skeleton } from "@/components/ui/skeleton";

export default function LoginPage() {
  return (
    <Suspense
      fallback={
        <LoadingState
          label="Loading sign-in form…"
          className="h-[28rem] w-full max-w-md"
        >
          <Skeleton className="h-full w-full rounded-2xl" />
        </LoadingState>
      }
    >
      <LoginView />
    </Suspense>
  );
}
