import Link from "next/link";
import { CheckCircle2 } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";

export default function VerifyPage() {
  return <Card className="w-full max-w-md border-stealth-border/80 bg-stealth-panel"><CardHeader className="p-7 pb-4"><CheckCircle2 className="mb-4 size-6 text-cyan-300" /><CardTitle className="text-2xl">Check your email</CardTitle><CardDescription>Use the one-time verification link from Stealth to finish setting up your account.</CardDescription></CardHeader><CardContent className="p-7 pt-2"><p className="text-sm leading-6 text-slate-500">Verification tokens are consumed by the Go API and are never persisted in the console.</p><Link href="/login" className="mt-6 inline-flex text-sm font-medium text-cyan-300 hover:text-cyan-200">Return to sign in →</Link></CardContent></Card>;
}
