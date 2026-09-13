import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-lg text-sm font-medium transition-colors disabled:pointer-events-none disabled:opacity-45 focus-visible:ring-2 focus-visible:ring-cyan-300/40",
  {
    variants: {
      variant: {
        default: "bg-cyan-300 text-slate-950 hover:bg-cyan-200",
        secondary:
          "border border-stealth-border bg-stealth-elevated text-slate-100 hover:bg-white/[0.08]",
        ghost: "text-slate-300 hover:bg-white/[0.06] hover:text-white",
        outline:
          "border border-stealth-border bg-transparent text-slate-200 hover:border-slate-500 hover:bg-white/[0.04]",
        destructive: "bg-rose-500/90 text-white hover:bg-rose-500",
        subtle:
          "bg-white/[0.04] text-slate-300 hover:bg-white/[0.08] hover:text-white",
      },
      size: {
        default: "min-h-11 px-3.5",
        sm: "min-h-11 rounded-md px-2.5 text-xs",
        lg: "h-11 px-5",
        icon: "size-11",
      },
    },
    defaultVariants: { variant: "default", size: "default" },
  },
);

export interface ButtonProps
  extends
    React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean;
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : "button";
    return (
      <Comp
        ref={ref}
        className={cn(buttonVariants({ variant, size, className }))}
        {...props}
      />
    );
  },
);
Button.displayName = "Button";

export { Button, buttonVariants };
