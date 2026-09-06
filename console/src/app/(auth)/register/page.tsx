"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { zodResolver } from "@hookform/resolvers/zod";
import { ArrowRight, Loader2 } from "lucide-react";
import { useRegister } from "@/api/mutations";
import { errorMessage } from "@/components/feedback/error-state";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";

const schema = z.object({ email: z.string().email("Enter a valid email address."), password: z.string().min(12, "Use at least 12 characters.") });
type FormValues = z.infer<typeof schema>;

export default function RegisterPage() {
  const router = useRouter();
  const mutation = useRegister();
  const form = useForm<FormValues>({ resolver: zodResolver(schema), defaultValues: { email: "", password: "" } });
  const submit = form.handleSubmit(async (values) => { await mutation.mutateAsync(values); router.replace("/organizations"); });
  return <Card className="w-full max-w-md border-stealth-border/80 bg-stealth-panel"><CardHeader className="p-7 pb-4"><p className="mb-2 text-xs font-medium uppercase tracking-[0.18em] text-cyan-300">Start building</p><CardTitle className="text-2xl">Create your Console</CardTitle><CardDescription>Your first organization is created with the account.</CardDescription></CardHeader><CardContent className="p-7 pt-2"><form onSubmit={submit} className="space-y-4"><div className="space-y-2"><Label htmlFor="email">Email</Label><Input id="email" type="email" autoComplete="email" {...form.register("email")} />{form.formState.errors.email ? <p className="text-xs text-rose-300">{form.formState.errors.email.message}</p> : null}</div><div className="space-y-2"><Label htmlFor="password">Password</Label><Input id="password" type="password" autoComplete="new-password" {...form.register("password")} /><p className="text-xs text-slate-600">Minimum 12 characters.</p>{form.formState.errors.password ? <p className="text-xs text-rose-300">{form.formState.errors.password.message}</p> : null}</div>{mutation.error ? <p className="rounded-lg border border-rose-300/20 bg-rose-400/10 px-3 py-2 text-xs leading-5 text-rose-200">{errorMessage(mutation.error)}</p> : null}<Button className="mt-2 h-10 w-full" disabled={mutation.isPending}>{mutation.isPending ? <Loader2 className="size-4 animate-spin" /> : <ArrowRight className="size-4" />}{mutation.isPending ? "Creating…" : "Create account"}</Button></form><p className="mt-6 text-center text-sm text-slate-500">Already have access? <Link href="/login" className="font-medium text-cyan-300 hover:text-cyan-200">Sign in</Link></p></CardContent></Card>;
}
