import * as React from "react";
import { cn } from "@/lib/utils";

export const Textarea = React.forwardRef<
  HTMLTextAreaElement,
  React.TextareaHTMLAttributes<HTMLTextAreaElement>
>(({ className, ...props }, ref) => (
  <textarea
    ref={ref}
    className={cn(
      "min-h-24 w-full rounded-md border border-graphite bg-carbon px-3.5 py-2.5 text-sm text-mist transition-colors duration-150 focus:border-acid-lime/70 focus:ring-2 focus:ring-acid-lime/15",
      className,
    )}
    {...props}
  />
));
Textarea.displayName = "Textarea";
