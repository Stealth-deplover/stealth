import { Suspense } from "react";
import { LoginView } from "@/features/auth/login-view";
import { Skeleton } from "@/components/ui/skeleton";

export default function LoginPage() {
  return <Suspense fallback={<Skeleton className="h-[28rem] w-full max-w-md rounded-2xl" />}><LoginView /></Suspense>;
}
