import type { LucideIcon } from "lucide-react";
import { Card, CardContent } from "@/components/ui/card";

export function EmptyState({
  icon: Icon,
  title,
  detail,
}: {
  icon: LucideIcon;
  title: string;
  detail: string;
}) {
  return (
    <Card className="gap-0 border-dashed py-0 shadow-none">
      <CardContent className="grid min-h-[360px] place-items-center text-center">
        <div>
          <div className="mx-auto grid size-12 place-items-center rounded-md border bg-muted/40 text-muted-foreground">
            <Icon size={20} strokeWidth={1.6} />
          </div>
          <h3 className="mt-4 text-sm font-medium">{title}</h3>
          <p className="mt-1.5 text-xs text-muted-foreground">{detail}</p>
        </div>
      </CardContent>
    </Card>
  );
}
