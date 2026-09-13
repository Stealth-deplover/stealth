import { Suspense } from "react";
import { BrowserSetupView } from "@/features/auth/browser-setup-view";
import { Skeleton } from "@/components/ui/skeleton";

export default function SetupPage() {
  return (
    <Suspense
      fallback={<Skeleton className="h-[36rem] w-full max-w-5xl rounded-2xl" />}
    >
      <BrowserSetupView />
    </Suspense>
  );
}
