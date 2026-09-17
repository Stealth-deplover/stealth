import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md text-[13px] font-medium tracking-[-0.01em] transition-colors duration-150 disabled:pointer-events-none disabled:opacity-45 focus-visible:ring-2 focus-visible:ring-acid-lime/50 focus-visible:ring-offset-2 focus-visible:ring-offset-void",
  {
    variants: {
      variant: {
        default: "bg-acid-lime text-void hover:bg-bone",
        secondary:
          "border border-graphite bg-obsidian text-mist hover:border-smoke hover:bg-graphite",
        ghost: "text-mist hover:bg-white/[0.06] hover:text-paper",
        outline:
          "border border-graphite bg-transparent text-mist hover:border-smoke hover:bg-white/[0.04]",
        destructive: "bg-coral-red text-paper hover:bg-coral-red/85",
        subtle:
          "bg-white/[0.04] text-mist hover:bg-white/[0.08] hover:text-paper",
      },
      size: {
        default: "min-h-11 px-4",
        sm: "min-h-11 px-3 text-xs",
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
