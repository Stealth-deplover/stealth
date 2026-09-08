import { Suspense } from "react";
import { RegisterView } from "@/features/auth/register-view";
import { Skeleton } from "@/components/ui/skeleton";

export default function RegisterPage() {
  return (
    <Suspense
      fallback={<Skeleton className="h-[28rem] w-full max-w-md rounded-2xl" />}
    >
      <RegisterView />
    </Suspense>
  );
}
