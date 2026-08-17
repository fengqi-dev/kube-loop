import { cva, type VariantProps } from "class-variance-authority";
import { forwardRef, type ButtonHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

const buttonVariants = cva("inline-flex items-center justify-center gap-2 rounded-lg text-sm font-semibold transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50", {
  variants: {
    variant: {
      default: "bg-primary text-primary-foreground shadow-sm hover:bg-primary/90",
      outline: "border border-border bg-background/60 hover:bg-accent hover:text-accent-foreground",
      ghost: "hover:bg-accent hover:text-accent-foreground",
    },
    size: { default: "h-10 px-4 py-2", sm: "h-9 px-3", lg: "h-12 px-6 text-base" },
  },
  defaultVariants: { variant: "default", size: "default" },
});

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {}
export const Button = forwardRef<HTMLButtonElement, ButtonProps>(({ className, variant, size, ...props }, ref) => <button ref={ref} className={cn(buttonVariants({ variant, size }), className)} {...props} />);
Button.displayName = "Button";
export { buttonVariants };
