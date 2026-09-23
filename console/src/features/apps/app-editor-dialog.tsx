"use client";

import { useState, type FormEvent, type ReactNode } from "react";
import { toast } from "sonner";
import { errorMessage } from "@/components/feedback/error-state";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import type { StealthApp } from "@/api/types";
import { appFormValues, type AppFormValues } from "@/features/apps/app-form";

export function AppEditorDialog({
  open,
  onOpenChange,
  app,
  pending,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  app?: StealthApp;
  pending?: boolean;
  onSubmit: (values: AppFormValues) => Promise<void>;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        {open ? (
          <AppEditorForm
            key={app?.id ?? "new"}
            app={app}
            pending={pending}
            onOpenChange={onOpenChange}
            onSubmit={onSubmit}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function AppEditorForm({
  app,
  pending,
  onOpenChange,
  onSubmit,
}: {
  app?: StealthApp;
  pending?: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (values: AppFormValues) => Promise<void>;
}) {
  const [values, setValues] = useState<AppFormValues>(() => appFormValues(app));

  const update = (field: keyof AppFormValues, value: string) => {
    setValues((current) => ({ ...current, [field]: value }));
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    try {
      await onSubmit(values);
      onOpenChange(false);
    } catch (error) {
      toast.error(errorMessage(error));
    }
  };

  return (
    <>
        <DialogHeader>
          <DialogTitle>{app ? "Edit App configuration" : "Create an App"}</DialogTitle>
          <DialogDescription>
            Set durable runtime intent. WorkloadSpec values left blank use the server’s v1 defaults.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-5">
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Name" htmlFor="app-name" help="Lowercase project resource slug.">
              <Input
                id="app-name"
                autoComplete="off"
                required
                minLength={2}
                maxLength={63}
                value={values.name}
                onChange={(event) => update("name", event.target.value)}
                placeholder="backend"
              />
            </Field>
            <Field label="Desired state" htmlFor="app-enabled">
              <select
                id="app-enabled"
                value={values.enabled}
                onChange={(event) => update("enabled", event.target.value)}
                className="flex min-h-11 w-full rounded-md border border-graphite bg-carbon px-3.5 text-sm text-mist outline-none focus-visible:ring-2 focus-visible:ring-acid-lime/40"
              >
                <option value="true">Enabled</option>
                <option value="false">Disabled</option>
              </select>
            </Field>
            <NumberField
              label="Internal HTTP port"
              field="port"
              values={values}
              update={update}
              min={1}
              max={65535}
              placeholder="8080"
              help="Future App routing targets this in-container port. No host port is opened."
            />
            <Field label="Working directory" htmlFor="app-working-directory" help="Absolute POSIX path inside the container.">
              <Input
                id="app-working-directory"
                value={values.working_directory}
                onChange={(event) => update("working_directory", event.target.value)}
                placeholder="/srv/app"
                autoComplete="off"
              />
            </Field>
            <Field label="Health protocol" htmlFor="app-health-protocol">
              <select
                id="app-health-protocol"
                value={values.health_protocol}
                onChange={(event) => {
                  update("health_protocol", event.target.value);
                  if (event.target.value !== "http") update("health_path", "");
                }}
                className="flex min-h-11 w-full rounded-md border border-graphite bg-carbon px-3.5 text-sm text-mist outline-none focus-visible:ring-2 focus-visible:ring-acid-lime/40"
              >
                <option value="">Use default (TCP)</option>
                <option value="tcp">TCP</option>
                <option value="http">HTTP</option>
              </select>
            </Field>
            {values.health_protocol === "http" ? (
              <Field label="Health path" htmlFor="app-health-path" help="Local path on the App’s internal port, such as /healthz.">
                <Input
                  id="app-health-path"
                  required
                  maxLength={2048}
                  value={values.health_path}
                  onChange={(event) => update("health_path", event.target.value)}
                  placeholder="/healthz"
                  autoComplete="off"
                />
              </Field>
            ) : null}
          </div>

          <Field
            label="Command arguments"
            htmlFor="app-command-arguments"
            help="One nonblank line per exec argument. Whitespace and quotes are passed literally; no shell parsing or expansion is performed. Leave empty to use the image default command."
          >
            <Textarea
              id="app-command-arguments"
              rows={4}
              value={values.command_arguments}
              onChange={(event) => update("command_arguments", event.target.value)}
              placeholder={'./server\n--port\n8080'}
              spellCheck={false}
              className="font-mono text-xs"
            />
          </Field>

          <div>
            <h3 className="mb-3 text-xs font-semibold uppercase tracking-[0.12em] text-fog">Resource limits</h3>
            <div className="grid gap-4 sm:grid-cols-3">
              <NumberField label="CPU (millicores)" field="cpu_millis" values={values} update={update} min={50} max={8000} placeholder="500" />
              <NumberField label="Memory (bytes)" field="memory_bytes" values={values} update={update} min={67108864} max={17179869184} placeholder="536870912" />
              <NumberField label="PIDs limit" field="pids_limit" values={values} update={update} min={16} max={2048} placeholder="256" />
            </div>
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <NumberField
              label="Stop grace period (seconds)"
              field="stop_grace_period_seconds"
              values={values}
              update={update}
              min={1}
              max={120}
              placeholder="15"
            />
            <div className="space-y-2">
              <Label htmlFor="app-restart-policy">Restart policy</Label>
              <Input id="app-restart-policy" value="Always" readOnly aria-readonly="true" />
              <p className="text-[11px] leading-5 text-fog">Stealth fixes this v1 policy.</p>
            </div>
          </div>

          <div className="rounded-md border border-graphite bg-white/[0.02] px-3.5 py-3 text-xs leading-5 text-fog">
            Apps store desired configuration only. Saving does not build an image or start a process.
          </div>

          <DialogFooter>
            <Button type="button" variant="ghost" disabled={pending} onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={pending}>
              {pending ? "Saving…" : app ? "Save changes" : "Create App"}
            </Button>
          </DialogFooter>
        </form>
    </>
  );
}

function Field({
  label,
  htmlFor,
  help,
  children,
}: {
  label: string;
  htmlFor: string;
  help?: string;
  children: ReactNode;
}) {
  return (
    <div className="space-y-2">
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
      {help ? <p className="text-[11px] leading-5 text-fog">{help}</p> : null}
    </div>
  );
}

function NumberField({
  label,
  field,
  values,
  update,
  min,
  max,
  placeholder,
  help,
}: {
  label: string;
  field: "port" | "cpu_millis" | "memory_bytes" | "pids_limit" | "stop_grace_period_seconds";
  values: AppFormValues;
  update: (field: keyof AppFormValues, value: string) => void;
  min: number;
  max: number;
  placeholder: string;
  help?: string;
}) {
  const id = `app-${field}`;
  return (
    <Field label={label} htmlFor={id} help={help}>
      <Input
        id={id}
        type="number"
        inputMode="numeric"
        step={1}
        min={min}
        max={max}
        value={values[field]}
        onChange={(event) => update(field, event.target.value)}
        placeholder={placeholder}
      />
    </Field>
  );
}
