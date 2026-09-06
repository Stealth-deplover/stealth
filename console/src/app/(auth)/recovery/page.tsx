"use client";

import Link from "next/link";
import { useState } from "react";
import { z } from "zod";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { CheckCircle2, Loader2, Mail } from "lucide-react";
import { useRecoveryRequest } from "@/api/mutations";
import { errorMessage } from "@/components/feedback/error-state";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";

const schema = z.object({ email: z.string().email("Enter a valid email address.") });
type FormValues = z.infer<typeof schema>;

export default function RecoveryPage() {
  const [sent, setSent] = useState(false);
  const mutation = useRecoveryRequest();
  const form = useForm<FormValues>({ resolver: zodResolver(schema), defaultValues: { email: "" } });
  const submit = form.handleSubmit(async (values) => { await mutation.mutateAsync(values); setSent(true); });
  return <Card className="w-full max-w-md border-stealth-border/80 bg-stealth-panel"><CardHeader className="p-7 pb-4"><p className="mb-2 text-xs font-medium uppercase tracking-[0.18em] text-cyan-300">Account recovery</p><CardTitle className="text-2xl">Reset your password</CardTitle><CardDescription>We will send a one-time link if the account exists.</CardDescription></CardHeader><CardContent className="p-7 pt-2">{sent ? <div className="rounded-xl border border-emerald-300/20 bg-emerald-300/10 p-5"><CheckCircle2 className="mb-3 size-5 text-emerald-300" /><p className="text-sm font-medium text-emerald-100">Recovery request accepted</p><p className="mt-1 text-xs leading-5 text-emerald-200/70">Check your inbox for the one-time recovery link. For account privacy, this response is the same whether the address exists.</p></div> : <form onSubmit={submit} className="space-y-4"><div className="space-y-2"><Label htmlFor="email">Email</Label><Input id="email" type="email" autoComplete="email" {...form.register("email")} />{form.formState.errors.email ? <p className="text-xs text-rose-300">{form.formState.errors.email.message}</p> : null}</div>{mutation.error ? <p className="rounded-lg border border-rose-300/20 bg-rose-400/10 px-3 py-2 text-xs leading-5 text-rose-200">{errorMessage(mutation.error)}</p> : null}<Button className="h-10 w-full" disabled={mutation.isPending}>{mutation.isPending ? <Loader2 className="size-4 animate-spin" /> : <Mail className="size-4" />}{mutation.isPending ? "Sending…" : "Email recovery link"}</Button></form>}<p className="mt-6 text-center text-sm text-slate-500"><Link href="/login" className="font-medium text-cyan-300 hover:text-cyan-200">Back to sign in</Link></p></CardContent></Card>;
}
