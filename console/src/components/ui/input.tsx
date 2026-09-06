import * as React from "react";
import { cn } from "@/lib/utils";

export const Input = React.forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(({ className, ...props }, ref) => (
  <input ref={ref} className={cn("h-9 w-full rounded-lg border border-stealth-border bg-black/20 px-3 text-sm text-white placeholder:text-slate-600 focus:border-cyan-300/60", className)} {...props} />
));
Input.displayName = "Input";
