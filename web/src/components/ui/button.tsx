import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

// shadcn/ui Button structure (cva variants + asChild), themed with the project's
// existing warm-paper tokens instead of shadcn's parallel --primary/--accent
// layer — so it inherits the current look and there's a single source of truth.
const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-lg text-[13px] font-medium transition outline-none focus-visible:ring-2 focus-visible:ring-accent/50 disabled:pointer-events-none disabled:opacity-40 [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        default: "bg-accent text-white hover:brightness-105",
        soft: "bg-accentsoft text-accent hover:brightness-[1.02]",
        outline: "border border-border bg-surface text-muted hover:bg-surface2 hover:text-ink",
        secondary: "bg-surface2 text-ink hover:brightness-[0.98]",
        ghost: "text-muted hover:bg-surface2 hover:text-ink",
        link: "text-accent underline-offset-2 hover:underline",
      },
      size: {
        default: "h-9 px-3.5 py-2",
        sm: "h-8 px-3",
        lg: "h-10 px-5",
        icon: "h-8 w-8",
      },
    },
    defaultVariants: { variant: "default", size: "default" },
  },
);

function Button({
  className,
  variant,
  size,
  asChild = false,
  ...props
}: React.ComponentProps<"button"> & VariantProps<typeof buttonVariants> & { asChild?: boolean }) {
  const Comp = asChild ? Slot : "button";
  return <Comp className={cn(buttonVariants({ variant, size }), className)} {...props} />;
}

export { Button, buttonVariants };
