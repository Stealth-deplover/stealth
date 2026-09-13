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

export type CreateFormValues = Record<string, string>;

export type CreateField<TValues extends CreateFormValues = CreateFormValues> = {
  name: Extract<keyof TValues, string>;
  label: string;
  placeholder?: string;
  type?:
    | "text"
    | "email"
    | "password"
    | "url"
    | "datetime-local"
    | "textarea"
    | "select"
    | "multiselect";
  options?: readonly { value: string; label: string }[];
  optionsForValues?: (
    values: Readonly<TValues>,
  ) => readonly { value: string; label: string }[];
  onChange?: (value: string, values: Readonly<TValues>) => Partial<TValues>;
  defaultValue?: string;
  required?: boolean;
  help?: string;
};

function createInitialValues<TValues extends CreateFormValues>(
  fields: readonly CreateField<TValues>[],
): TValues {
  return Object.fromEntries(
    fields.map((field) => [field.name, field.defaultValue ?? ""]),
  ) as TValues;
}

type CreateDialogProps<TValues extends CreateFormValues> = {
  triggerLabel: string;
  submitLabel: string;
  pendingLabel: string;
  title: string;
  description: string;
  fields: readonly CreateField<TValues>[];
  onSubmit: (values: TValues) => Promise<void> | void;
  pending?: boolean;
  disabled?: boolean;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  trigger?: ReactNode;
};

export function CreateDialog<
  TValues extends CreateFormValues = CreateFormValues,
>({
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
}: CreateDialogProps<TValues>) {
  const [internalOpen, setInternalOpen] = useState(false);
  const [values, setValues] = useState<TValues>(() =>
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
  const updateValue = (name: Extract<keyof TValues, string>, value: string) =>
    setValues((current) => {
      const field = fields.find((candidate) => candidate.name === name);
      const next = {
        ...current,
        [name]: value,
      } as TValues;
      const dependentValues = field?.onChange?.(value, current);
      if (dependentValues) Object.assign(next, dependentValues);
      return next;
    });
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
                  className="flex min-h-11 w-full rounded-lg border border-stealth-border bg-stealth-panel px-3 text-sm text-white outline-none focus-visible:ring-2 focus-visible:ring-cyan-300/40"
                >
                  {(field.optionsForValues?.(values) ?? field.options)?.map(
                    (option) => (
                      <option key={option.value} value={option.value}>
                        {option.label}
                      </option>
                    ),
                  )}
                </select>
              ) : field.type === "multiselect" ? (
                <div
                  id={field.name}
                  role="group"
                  aria-label={field.label}
                  className="grid gap-2 rounded-lg border border-stealth-border bg-stealth-panel p-3 sm:grid-cols-2"
                >
                  {field.options?.map((option) => {
                    const selected = (values[field.name] ?? "")
                      .split(",")
                      .map((value) => value.trim())
                      .includes(option.value);
                    return (
                      <label
                        key={option.value}
                        className="flex cursor-pointer items-start gap-2 rounded-md px-2 py-1.5 text-xs text-slate-300 hover:bg-white/[0.04]"
                      >
                        <Input
                          type="checkbox"
                          aria-label={option.label}
                          checked={selected}
                          onChange={(event) => {
                            const next = new Set(
                              (values[field.name] ?? "")
                                .split(",")
                                .map((value) => value.trim())
                                .filter(Boolean),
                            );
                            if (event.target.checked) next.add(option.value);
                            else next.delete(option.value);
                            updateValue(field.name, Array.from(next).join(","));
                          }}
                          className="mt-0.5 size-4 accent-cyan-300"
                        />
                        <span className="min-w-0">
                          <span className="block text-slate-200">
                            {option.label}
                          </span>
                          <span className="font-mono text-[10px] text-slate-600">
                            {option.value}
                          </span>
                        </span>
                      </label>
                    );
                  })}
                </div>
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
