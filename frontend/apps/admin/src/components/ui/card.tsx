import * as React from "react";
import { cn } from "@/lib/utils";

function Card({ className, ...props }: React.ComponentProps<"div">) { return <div data-slot="card" className={cn("flex flex-col rounded-xl border border-border bg-card text-card-foreground shadow-sm", className)} {...props} />; }
function CardContent({ className, ...props }: React.ComponentProps<"div">) { return <div data-slot="card-content" className={cn("p-4", className)} {...props} />; }

export { Card, CardContent };
