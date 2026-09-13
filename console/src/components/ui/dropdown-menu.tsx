"use client";

import * as DropdownMenuPrimitive from "@radix-ui/react-dropdown-menu";
import { cn } from "@/lib/utils";

export const DropdownMenu = DropdownMenuPrimitive.Root;
export const DropdownMenuTrigger = DropdownMenuPrimitive.Trigger;
export const DropdownMenuGroup = DropdownMenuPrimitive.Group;

export const DropdownMenuContent = ({
  className,
  sideOffset = 6,
  ...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Content>) => (
  <DropdownMenuPrimitive.Portal>
    <DropdownMenuPrimitive.Content
      sideOffset={sideOffset}
      className={cn(
        "z-50 min-w-44 rounded-xl border border-stealth-border bg-stealth-elevated p-1.5 text-sm shadow-2xl",
        className,
      )}
      {...props}
    />
  </DropdownMenuPrimitive.Portal>
);
export const DropdownMenuItem = ({
  className,
  ...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Item>) => (
  <DropdownMenuPrimitive.Item
    className={cn(
      "flex min-h-11 cursor-pointer items-center gap-2 rounded-lg px-2.5 py-2 text-slate-300 outline-none hover:bg-white/[0.07] hover:text-white data-[highlighted]:bg-cyan-300/10 data-[highlighted]:text-white data-[highlighted]:ring-2 data-[highlighted]:ring-inset data-[highlighted]:ring-cyan-300/40",
      className,
    )}
    {...props}
  />
);
export const DropdownMenuLabel = ({
  className,
  ...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Label>) => (
  <DropdownMenuPrimitive.Label
    className={cn(
      "px-2.5 py-1.5 text-[11px] font-medium uppercase tracking-[0.16em] text-slate-600",
      className,
    )}
    {...props}
  />
);
export const DropdownMenuSeparator = ({
  className,
  ...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Separator>) => (
  <DropdownMenuPrimitive.Separator
    className={cn("-mx-1 my-1 h-px bg-stealth-border", className)}
    {...props}
  />
);
