import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const alertVariants = cva("relative grid w-full gap-1 rounded-lg border px-3 py-2 text-sm has-[>svg]:grid-cols-[auto_1fr] has-[>svg]:gap-x-2", { variants: { variant: { default: "bg-card text-card-foreground", destructive: "border-destructive/30 bg-destructive/10 text-destructive" } }, defaultVariants: { variant: "default" } });
function Alert({ className, variant, ...props }: React.ComponentProps<"div"> & VariantProps<typeof alertVariants>) { return <div data-slot="alert" role="alert" className={cn(alertVariants({ variant }), className)} {...props} />; }
function AlertDescription({ className, ...props }: React.ComponentProps<"div">) { return <div data-slot="alert-description" className={cn("text-sm", className)} {...props} />; }

export { Alert, AlertDescription };
