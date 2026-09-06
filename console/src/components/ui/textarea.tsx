import * as React from "react";
import { cn } from "@/lib/utils";

export const Textarea = React.forwardRef<HTMLTextAreaElement, React.TextareaHTMLAttributes<HTMLTextAreaElement>>(({ className, ...props }, ref) => (
  <textarea ref={ref} className={cn("min-h-24 w-full rounded-lg border border-stealth-border bg-black/20 px-3 py-2 text-sm text-white placeholder:text-slate-600 focus:border-cyan-300/60", className)} {...props} />
));
Textarea.displayName = "Textarea";
