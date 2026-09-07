"use client";

import { Check, Copy, Eye, EyeOff, ShieldAlert } from "lucide-react";
import { useRef, useState } from "react";
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

export function maskSecret(secret: string) {
  if (secret.length <= 4) return "•".repeat(12);
  return `${secret.slice(0, 4)}${"•".repeat(Math.max(12, Math.min(32, secret.length)))}`;
}

export function OneTimeSecretDialog({
  secret,
  title = "Copy this secret now",
  description = "This secret will not be shown again.",
  onDone,
}: {
  secret: string | null;
  title?: string;
  description?: string;
  onDone: () => void;
}) {
  const [revealed, setRevealed] = useState(false);
  const [acknowledged, setAcknowledged] = useState(false);
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">(
    "idle",
  );
  const completed = useRef(false);

  const close = () => {
    if (acknowledged && !completed.current) {
      completed.current = true;
      onDone();
    }
  };

  const copy = async () => {
    if (!secret) return;
    try {
      if (!navigator.clipboard) throw new Error("Clipboard API unavailable");
      await navigator.clipboard.writeText(secret);
      setCopyState("copied");
    } catch {
      setCopyState("failed");
    }
  };

  return (
    <Dialog
      open={Boolean(secret)}
      onOpenChange={(open) => {
        if (!open) close();
      }}
    >
      <DialogContent
        showClose={acknowledged}
        onEscapeKeyDown={(event) => {
          if (!acknowledged) event.preventDefault();
        }}
        onPointerDownOutside={(event) => {
          if (!acknowledged) event.preventDefault();
        }}
        onInteractOutside={(event) => {
          if (!acknowledged) event.preventDefault();
        }}
      >
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <ShieldAlert className="size-4 text-amber-300" />
            {title}
          </DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <div className="rounded-lg border border-amber-300/20 bg-amber-300/10 p-4">
          <p className="text-xs font-medium text-amber-100">
            Save this value in a password manager or secure deployment secret.
          </p>
          <code
            className="mt-3 block select-all break-all rounded-md border border-amber-300/10 bg-black/20 p-3 font-mono text-xs text-amber-50"
            data-testid="one-time-secret"
          >
            {secret ? (revealed ? secret : maskSecret(secret)) : ""}
          </code>
          <div className="mt-3 flex flex-wrap gap-2">
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => setRevealed((current) => !current)}
            >
              {revealed ? (
                <EyeOff className="size-3.5" />
              ) : (
                <Eye className="size-3.5" />
              )}
              {revealed ? "Hide" : "Reveal"}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => void copy()}
            >
              <Copy className="size-3.5" />
              {copyState === "copied" ? "Copied" : "Copy"}
            </Button>
          </div>
          {copyState === "failed" ? (
            <p className="mt-2 text-xs text-amber-200">
              Clipboard access failed. Reveal the value and copy it manually
              from the selectable text.
            </p>
          ) : null}
        </div>
        <div className="mt-5 flex items-start gap-3">
          <Input
            id="secret-acknowledged"
            type="checkbox"
            aria-label="I have saved this secret safely"
            checked={acknowledged}
            onChange={(event) => setAcknowledged(event.target.checked)}
            className="mt-0.5 size-4 accent-cyan-300"
          />
          <Label
            htmlFor="secret-acknowledged"
            className="text-sm leading-5 text-slate-300"
          >
            I have saved this secret safely.
          </Label>
        </div>
        <DialogFooter>
          <Button type="button" disabled={!acknowledged} onClick={close}>
            <Check className="size-4" /> Done
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
