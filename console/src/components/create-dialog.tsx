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

export type CreateField = { name: string; label: string; placeholder?: string; type?: "text" | "email" | "password" | "url" | "textarea"; defaultValue?: string; required?: boolean; help?: string };

export function CreateDialog({ label = "Create", title, description, fields, onSubmit, pending }: { label?: string; title: string; description: string; fields: CreateField[]; onSubmit: (values: Record<string, string>) => Promise<void> | void; pending?: boolean }) {
  const [open, setOpen] = useState(false);
  const [values, setValues] = useState<Record<string, string>>(() => Object.fromEntries(fields.map((field) => [field.name, field.defaultValue ?? ""])));
  const submit = async (event: React.FormEvent<HTMLFormElement>) => { event.preventDefault(); try { await onSubmit(values); setOpen(false); setValues(Object.fromEntries(fields.map((field) => [field.name, field.defaultValue ?? ""]))); } catch (error) { toast.error(errorMessage(error)); } };
  return <Dialog open={open} onOpenChange={setOpen}><DialogTrigger asChild><Button><Plus className="size-4" /> {label}</Button></DialogTrigger><DialogContent><DialogHeader><DialogTitle>{title}</DialogTitle><DialogDescription>{description}</DialogDescription></DialogHeader><form onSubmit={submit} className="space-y-4">{fields.map((field) => <div key={field.name} className="space-y-2"><Label htmlFor={field.name}>{field.label}</Label>{field.type === "textarea" ? <Textarea id={field.name} required={field.required !== false} placeholder={field.placeholder} value={values[field.name] ?? ""} onChange={(event) => setValues((current) => ({ ...current, [field.name]: event.target.value }))} /> : <Input id={field.name} required={field.required !== false} type={field.type ?? "text"} placeholder={field.placeholder} value={values[field.name] ?? ""} onChange={(event) => setValues((current) => ({ ...current, [field.name]: event.target.value }))} />}{field.help ? <p className="text-[11px] text-slate-600">{field.help}</p> : null}</div>)}<Button type="submit" className="w-full" disabled={pending}>{pending ? <Loader2 className="size-4 animate-spin" /> : null}{pending ? "Creating…" : `Create ${label.toLowerCase()}`}</Button></form></DialogContent></Dialog>;
}
