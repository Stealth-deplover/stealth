"use client";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";

export function AppRollbackDialog({
  appName,
  currentVersion,
  targetVersion,
  open,
  pending,
  onOpenChange,
  onConfirm,
}: {
  appName: string;
  currentVersion: number;
  targetVersion: number;
  open: boolean;
  pending: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            Roll back {appName} from v{currentVersion} to v{targetVersion}?
          </DialogTitle>
          <DialogDescription>
            The App will converge to this earlier release asynchronously. The
            current route stays withdrawn until the rolled-back runtime passes a
            fresh health check.
          </DialogDescription>
        </DialogHeader>
        <ul className="space-y-2 text-sm leading-5 text-mist">
          <li>v{targetVersion}&apos;s immutable image becomes desired.</li>
          <li>Its captured runtime WorkloadSpec is restored.</li>
          <li>Current environment variables and secrets stay as-is.</li>
          <li>A new desired generation is created.</li>
          <li>Routing returns only after fresh health passes.</li>
        </ul>
        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            disabled={pending}
            onClick={() => onOpenChange(false)}
          >
            Cancel
          </Button>
          <Button type="button" disabled={pending} onClick={onConfirm}>
            {pending ? "Rolling back…" : `Rollback to v${targetVersion}`}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
