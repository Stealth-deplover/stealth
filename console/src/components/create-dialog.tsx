"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { Loader2, Plus } from "lucide-react";
import { toast } from "sonner";
import { errorMessage } from "@/components/feedback/error-state";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

export type CreateField = {
  name: string;
  label: string;
  placeholder?: string;
  type?: "text" | "email" | "password" | "url" | "textarea" | "select";
  options?: readonly { value: string; label: string }[];
  defaultValue?: string;
  required?: boolean;
  help?: string;
};

function createInitialValues(fields: CreateField[]) {
  return Object.fromEntries(
    fields.map((field) => [field.name, field.defaultValue ?? ""]),
  );
}

type CreateDialogProps = {
  triggerLabel: string;
  submitLabel: string;
  pendingLabel: string;
  title: string;
  description: string;
  fields: CreateField[];
  onSubmit: (values: Record<string, string>) => Promise<void> | void;
  pending?: boolean;
  disabled?: boolean;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  trigger?: ReactNode;
};

export function CreateDialog({
  triggerLabel,
  submitLabel,
  pendingLabel,
  title,
  description,
  fields,
  onSubmit,
  pending,
  disabled,
  open,
  onOpenChange,
  trigger,
}: CreateDialogProps) {
  const [internalOpen, setInternalOpen] = useState(false);
  const [values, setValues] = useState<Record<string, string>>(() =>
    createInitialValues(fields),
  );
  const controlled = open !== undefined;
  const dialogOpen = open ?? internalOpen;
  const setDialogOpen = (nextOpen: boolean) => {
    onOpenChange?.(nextOpen);
    if (!controlled) setInternalOpen(nextOpen);
  };
  const resetValues = useCallback(
    () => setValues(createInitialValues(fields)),
    [fields],
  );
  const handleOpenChange = (nextOpen: boolean) => {
    setDialogOpen(nextOpen);
    if (!nextOpen) resetValues();
  };
  const previousOpen = useRef(dialogOpen);
  useEffect(() => {
    if (previousOpen.current && !dialogOpen) resetValues();
    previousOpen.current = dialogOpen;
  }, [dialogOpen, resetValues]);
  const updateValue = (name: string, value: string) =>
    setValues((current) => ({ ...current, [name]: value }));
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    try {
      await onSubmit(values);
      handleOpenChange(false);
    } catch (error) {
      toast.error(errorMessage(error));
    }
  };
  return (
    <Dialog open={dialogOpen} onOpenChange={handleOpenChange}>
      {trigger ? (
        <DialogTrigger asChild>{trigger}</DialogTrigger>
      ) : (
        <DialogTrigger asChild>
          <Button disabled={disabled}>
            <Plus className="size-4" /> {triggerLabel}
          </Button>
        </DialogTrigger>
      )}
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          {fields.map((field) => (
            <div key={field.name} className="space-y-2">
              <Label htmlFor={field.name}>{field.label}</Label>
              {field.type === "textarea" ? (
                <Textarea
                  id={field.name}
                  required={field.required !== false}
                  placeholder={field.placeholder}
                  value={values[field.name] ?? ""}
                  onChange={(event) =>
                    updateValue(field.name, event.target.value)
                  }
                />
              ) : field.type === "select" ? (
                <select
                  id={field.name}
                  required={field.required !== false}
                  value={values[field.name] ?? ""}
                  onChange={(event) =>
                    updateValue(field.name, event.target.value)
                  }
                  className="flex h-10 w-full rounded-lg border border-stealth-border bg-stealth-panel px-3 text-sm text-white outline-none focus-visible:ring-2 focus-visible:ring-cyan-300/40"
                >
                  {field.options?.map((option) => (
                    <option key={option.value} value={option.value}>
                      {option.label}
                    </option>
                  ))}
                </select>
              ) : (
                <Input
                  id={field.name}
                  required={field.required !== false}
                  type={field.type ?? "text"}
                  placeholder={field.placeholder}
                  value={values[field.name] ?? ""}
                  onChange={(event) =>
                    updateValue(field.name, event.target.value)
                  }
                />
              )}
              {field.help ? (
                <p className="text-[11px] leading-5 text-slate-600">
                  {field.help}
                </p>
              ) : null}
            </div>
          ))}
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={() => handleOpenChange(false)}
              disabled={pending}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={pending}>
              {pending ? <Loader2 className="size-4 animate-spin" /> : null}
              {pending ? pendingLabel : submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
