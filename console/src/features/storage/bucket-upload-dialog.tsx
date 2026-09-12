"use client";

import { useState, type FormEvent } from "react";
import { toast } from "sonner";
import { useUploadStorageFile } from "@/api/mutations";
import type { StorageBucket, StorageFile } from "@/api/types";
import { ErrorState, errorMessage } from "@/components/feedback/error-state";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { formatBytes } from "@/lib/format";
import { objectName } from "./storage-values";

export function BucketUploadDialog({
  projectId,
  bucket,
  onUploaded,
  onClose,
}: {
  projectId: string;
  bucket: StorageBucket;
  onUploaded: (file?: StorageFile) => void;
  onClose: () => void;
}) {
  const upload = useUploadStorageFile(projectId, bucket.id);
  const [selected, setSelected] = useState<File>();
  const [validationError, setValidationError] = useState<string>();

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!selected || upload.isPending) return;
    try {
      objectName.parse(selected.name);
      if (selected.size > bucket.max_file_size_bytes)
        throw new Error(
          `The file exceeds this bucket's ${formatBytes(bucket.max_file_size_bytes)} per-object limit.`,
        );
      if (selected.size > bucket.quota_bytes - bucket.used_bytes)
        throw new Error("The file exceeds this bucket's remaining quota.");
      setValidationError(undefined);
    } catch (error) {
      setValidationError(errorMessage(error));
      return;
    }
    try {
      const result = await upload.mutateAsync(selected);
      toast.success("Object uploaded");
      onUploaded(result?.file);
    } catch (error) {
      toast.error(errorMessage(error));
    }
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !upload.isPending) onClose();
      }}
    >
      <DialogContent showClose={!upload.isPending}>
        <DialogHeader>
          <DialogTitle>Upload object</DialogTitle>
          <DialogDescription>
            Choose one file. Its filename becomes the object name; folders and
            paths are not supported.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <label className="grid gap-2 text-sm">
            Choose file
            <Input
              type="file"
              aria-label="Choose file"
              disabled={upload.isPending}
              onChange={(event) => {
                setSelected(event.target.files?.[0]);
                setValidationError(undefined);
                upload.reset();
              }}
            />
          </label>
          {selected ? (
            <div className="rounded-lg border border-stealth-border p-3 text-sm">
              <p className="break-all">{selected.name}</p>
              <p className="mt-1 text-slate-500">
                {formatBytes(selected.size)}
              </p>
            </div>
          ) : null}
          <p className="text-xs text-slate-500">
            Maximum object size: {formatBytes(bucket.max_file_size_bytes)}.
            Available quota:{" "}
            {formatBytes(Math.max(0, bucket.quota_bytes - bucket.used_bytes))}.
          </p>
          {validationError ? (
            <p role="alert" className="text-sm text-rose-300">
              {validationError}
            </p>
          ) : null}
          {upload.error ? (
            <ErrorState title="Could not upload object" error={upload.error} />
          ) : null}
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              disabled={upload.isPending}
              onClick={onClose}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={!selected || upload.isPending}>
              {upload.isPending ? "Uploading object…" : "Upload object"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
