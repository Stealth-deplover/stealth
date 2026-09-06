"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { zodResolver } from "@hookform/resolvers/zod";
import { ArrowRight, Loader2 } from "lucide-react";
import { useLogin } from "@/api/mutations";
import { errorMessage } from "@/components/feedback/error-state";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";

const schema = z.object({ email: z.string().email("Enter a valid email address."), password: z.string().min(1, "Enter your password.") });
type FormValues = z.infer<typeof schema>;

export function LoginView() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const mutation = useLogin();
  const form = useForm<FormValues>({ resolver: zodResolver(schema), defaultValues: { email: "", password: "" } });
  const submit = form.handleSubmit(async (values) => { await mutation.mutateAsync(values); router.replace("/organizations"); });
  return <Card className="w-full max-w-md border-stealth-border/80 bg-stealth-panel"><CardHeader className="p-7 pb-4"><p className="mb-2 text-xs font-medium uppercase tracking-[0.18em] text-cyan-300">Welcome back</p><CardTitle className="text-2xl">Sign in to Stealth</CardTitle><CardDescription>Use your Console account to access projects and services.</CardDescription></CardHeader><CardContent className="p-7 pt-2">{searchParams.get("reset") === "success" ? <p className="mb-4 rounded-lg border border-emerald-300/20 bg-emerald-300/10 px-3 py-2 text-xs leading-5 text-emerald-200">Password updated. Sign in with your new password.</p> : null}<form onSubmit={submit} className="space-y-4"><div className="space-y-2"><Label htmlFor="email">Email</Label><Input id="email" type="email" autoComplete="email" {...form.register("email")} />{form.formState.errors.email ? <p className="text-xs text-rose-300">{form.formState.errors.email.message}</p> : null}</div><div className="space-y-2"><div className="flex items-center justify-between"><Label htmlFor="password">Password</Label><Link href="/recovery" className="text-xs text-cyan-300 hover:text-cyan-200">Forgot password?</Link></div><Input id="password" type="password" autoComplete="current-password" {...form.register("password")} />{form.formState.errors.password ? <p className="text-xs text-rose-300">{form.formState.errors.password.message}</p> : null}</div>{mutation.error ? <p className="rounded-lg border border-rose-300/20 bg-rose-400/10 px-3 py-2 text-xs leading-5 text-rose-200">{errorMessage(mutation.error)}</p> : null}<Button className="mt-2 h-10 w-full" disabled={mutation.isPending}>{mutation.isPending ? <Loader2 className="size-4 animate-spin" /> : <ArrowRight className="size-4" />}{mutation.isPending ? "Signing in…" : "Continue"}</Button></form><p className="mt-6 text-center text-sm text-slate-500">New to Stealth? <Link href="/register" className="font-medium text-cyan-300 hover:text-cyan-200">Create an account</Link></p></CardContent></Card>;
}
