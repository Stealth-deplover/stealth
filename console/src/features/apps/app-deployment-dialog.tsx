"use client";

import { useState, type FormEvent } from "react";
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
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

export function AppDeploymentDialog({
  open,
  onOpenChange,
  pending,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  pending: boolean;
  onSubmit: (form: FormData) => Promise<void>;
}) {
  const [source, setSource] = useState<File | null>(null);
  const [dockerfilePath, setDockerfilePath] = useState("Dockerfile");
  const [contextDirectory, setContextDirectory] = useState(".");
  const [target, setTarget] = useState("");
  const [select, setSelect] = useState(false);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!source || !safeRelativePath(dockerfilePath) || !safeContext(contextDirectory)) {
      toast.error("Choose a source archive and enter safe relative build paths.");
      return;
    }
    if (target && !/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(target)) {
      toast.error("Target must be a short Dockerfile stage identifier.");
      return;
    }
    const form = new FormData();
    form.append("source", source);
    form.append("dockerfile_path", dockerfilePath);
    form.append("context_directory", contextDirectory);
    if (target.trim()) form.append("target", target.trim());
    form.append("select", select ? "true" : "false");
    try {
      await onSubmit(form);
      onOpenChange(false);
      setSource(null);
      setDockerfilePath("Dockerfile");
      setContextDirectory(".");
      setTarget("");
      setSelect(false);
    } catch (error) {
      toast.error(errorMessage(error));
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>Create App build deployment</DialogTitle>
          <DialogDescription>
            Upload a bounded source archive for an isolated BuildKit build. A
            verified image can be selected as desired state; the App will stay
            Not deployed because this release does not start workloads.
          </DialogDescription>
        </DialogHeader>
        <form className="space-y-5" onSubmit={submit}>
          <div className="space-y-2">
            <Label htmlFor="app-source-archive">Source archive</Label>
            <Input
              id="app-source-archive"
              type="file"
              accept=".zip,.tar,.tar.gz,.tgz"
              required
              disabled={pending}
              onChange={(event) => setSource(event.currentTarget.files?.[0] ?? null)}
            />
            <p className="text-[11px] leading-5 text-fog">
              ZIP, TAR, TAR.GZ, or TGZ. Archive paths and expanded size are
              validated by the control plane.
            </p>
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="app-dockerfile-path">Dockerfile path</Label>
              <Input
                id="app-dockerfile-path"
                value={dockerfilePath}
                maxLength={512}
                required
                autoComplete="off"
                spellCheck={false}
                disabled={pending}
                onChange={(event) => setDockerfilePath(event.target.value)}
              />
              <p className="text-[11px] text-fog">Relative to the archive root.</p>
            </div>
            <div className="space-y-2">
              <Label htmlFor="app-context-directory">Context directory</Label>
              <Input
                id="app-context-directory"
                value={contextDirectory}
                maxLength={512}
                required
                autoComplete="off"
                spellCheck={false}
                disabled={pending}
                onChange={(event) => setContextDirectory(event.target.value)}
              />
              <p className="text-[11px] text-fog">Use . for the archive root.</p>
            </div>
          </div>
          <div className="space-y-2">
            <Label htmlFor="app-build-target">Dockerfile target <span className="text-fog">(optional)</span></Label>
            <Input
              id="app-build-target"
              value={target}
              maxLength={128}
              autoComplete="off"
              spellCheck={false}
              disabled={pending}
              onChange={(event) => setTarget(event.target.value)}
              placeholder="release"
            />
          </div>
          <label className="flex items-start gap-3 rounded-md border border-graphite bg-white/[0.02] px-3.5 py-3 text-sm text-mist">
            <input
              type="checkbox"
              checked={select}
              disabled={pending}
              onChange={(event) => setSelect(event.target.checked)}
              className="mt-0.5 size-4 accent-lime-300"
            />
            <span>
              Select as desired image after a verified build
              <span className="mt-1 block text-xs leading-5 text-fog">
                Selection advances desired state only. It does not start a
                container or activate an App route.
              </span>
            </span>
          </label>
          <DialogFooter>
            <Button type="button" variant="ghost" disabled={pending} onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={pending || !source}>
              {pending ? "Uploading…" : "Queue build"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function safeRelativePath(value: string) {
  return canonicalRelativePath(value, false);
}

function safeContext(value: string) {
  return value === "." || canonicalRelativePath(value, false);
}

function canonicalRelativePath(value: string, allowDot: boolean) {
  if (
    !value ||
    value.length > 512 ||
    value.startsWith("/") ||
    value.includes("\\") ||
    /[\u0000-\u001f\u007f]/.test(value) ||
    value.trim() !== value
  ) {
    return false;
  }
  if (allowDot && value === ".") return true;
  return value.split("/").every((part) => part !== "" && part !== "." && part !== "..");
}
