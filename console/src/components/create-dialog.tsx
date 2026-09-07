"use client";

import { useState } from "react";
import { Loader2, Plus } from "lucide-react";
import { toast } from "sonner";
import { errorMessage } from "@/components/feedback/error-state";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

export type CreateField = { name: string; label: string; placeholder?: string; type?: "text" | "email" | "password" | "url" | "textarea" | "select"; options?: readonly { value: string; label: string }[]; defaultValue?: string; required?: boolean; help?: string };

export function CreateDialog({ label = "Create", title, description, fields, onSubmit, pending, disabled }: { label?: string; title: string; description: string; fields: CreateField[]; onSubmit: (values: Record<string, string>) => Promise<void> | void; pending?: boolean; disabled?: boolean }) {
  const [open, setOpen] = useState(false);
  const [values, setValues] = useState<Record<string, string>>(() => Object.fromEntries(fields.map((field) => [field.name, field.defaultValue ?? ""])));
  const submit = async (event: React.FormEvent<HTMLFormElement>) => { event.preventDefault(); try { await onSubmit(values); setOpen(false); setValues(Object.fromEntries(fields.map((field) => [field.name, field.defaultValue ?? ""]))); } catch (error) { toast.error(errorMessage(error)); } };
  const resetValues = () => setValues(Object.fromEntries(fields.map((field) => [field.name, field.defaultValue ?? ""])));
  return <Dialog open={open} onOpenChange={(value) => { setOpen(value); if (value) resetValues(); }}><DialogTrigger asChild><Button data-create-dialog disabled={disabled}><Plus className="size-4" /> {label}</Button></DialogTrigger><DialogContent><DialogHeader><DialogTitle>{title}</DialogTitle><DialogDescription>{description}</DialogDescription></DialogHeader><form onSubmit={submit} className="space-y-4">{fields.map((field) => <div key={field.name} className="space-y-2"><Label htmlFor={field.name}>{field.label}</Label>{field.type === "textarea" ? <Textarea id={field.name} required={field.required !== false} placeholder={field.placeholder} value={values[field.name] ?? ""} onChange={(event) => setValues((current) => ({ ...current, [field.name]: event.target.value }))} /> : field.type === "select" ? <select id={field.name} required={field.required !== false} value={values[field.name] ?? ""} onChange={(event) => setValues((current) => ({ ...current, [field.name]: event.target.value }))} className="flex h-10 w-full rounded-lg border border-stealth-border bg-stealth-panel px-3 text-sm text-white outline-none focus-visible:ring-2 focus-visible:ring-cyan-300/40">{field.options?.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select> : <Input id={field.name} required={field.required !== false} type={field.type ?? "text"} placeholder={field.placeholder} value={values[field.name] ?? ""} onChange={(event) => setValues((current) => ({ ...current, [field.name]: event.target.value }))} />}{field.help ? <p className="text-[11px] text-slate-600">{field.help}</p> : null}</div>)}<Button type="submit" className="w-full" disabled={pending}>{pending ? <Loader2 className="size-4 animate-spin" /> : null}{pending ? "Creating…" : `Create ${label.toLowerCase()}`}</Button></form></DialogContent></Dialog>;
}
