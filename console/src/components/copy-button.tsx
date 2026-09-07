"use client";

import { Check, Copy } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";

export function CopyButton({ value, label = "Copy", className }: { value: string; label?: string; className?: string }) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      setCopied(false);
    }
  };

  return <Button type="button" variant="ghost" size="icon" className={className} onClick={() => void copy()} aria-label={`${label} ${value}`} title={copied ? "Copied" : label}>
    {copied ? <Check className="size-3.5 text-emerald-300" /> : <Copy className="size-3.5" />}
  </Button>;
}
