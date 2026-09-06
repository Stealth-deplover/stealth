import { Suspense } from "react";
import { AccountVerificationView } from "@/features/auth/auth-flow-views";
import { Skeleton } from "@/components/ui/skeleton";

export default function VerifyEmailPage() { return <Suspense fallback={<Skeleton className="h-[20rem] w-full max-w-md rounded-2xl" />}><AccountVerificationView /></Suspense>; }
