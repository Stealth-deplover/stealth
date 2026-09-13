"use client";

import * as TabsPrimitive from "@radix-ui/react-tabs";
import { cn } from "@/lib/utils";

export const Tabs = TabsPrimitive.Root;
export const TabsList = ({
  className,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.List>) => (
  <TabsPrimitive.List
    className={cn(
      "inline-flex max-w-full items-center gap-1 overflow-x-auto border-b border-stealth-border",
      className,
    )}
    {...props}
  />
);
export const TabsTrigger = ({
  className,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Trigger>) => (
  <TabsPrimitive.Trigger
    className={cn(
      "inline-flex min-h-11 shrink-0 items-center justify-center border-b-2 border-transparent px-3 py-2 text-xs font-medium text-slate-500 transition hover:text-slate-200 data-[state=active]:border-cyan-300 data-[state=active]:text-cyan-200",
      className,
    )}
    {...props}
  />
);
export const TabsContent = ({
  className,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Content>) => (
  <TabsPrimitive.Content
    className={cn("mt-5 outline-none", className)}
    {...props}
  />
);
