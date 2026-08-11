import { Badge } from "@/components/ui/badge";
import { useI18n } from "@/i18n";
import { isBusyPhase, phaseKeys } from "@/lib/phase";
import { cn } from "@/lib/utils";
import type { SessionState } from "@/types";

export function StatusBadge({ phase }: { phase: SessionState["phase"] }) {
  const { t } = useI18n();
  const ready = phase === "connected";
  const working = isBusyPhase(phase);
  const error = phase === "error";

  return (
    <Badge
      variant="outline"
      className={cn(
        "h-8 gap-2 rounded-full px-3 text-[11px] font-medium",
        ready && "border-success/25 bg-success/10 text-success",
        error && "border-destructive/20 bg-destructive/10 text-destructive",
        working && "border-primary/25 bg-primary/10 text-primary",
        !ready && !error && !working && "text-muted-foreground",
      )}
    >
      <span
        className={cn(
          "size-1.5 rounded-full",
          ready && "bg-success",
          error && "bg-destructive",
          working && "animate-pulse bg-primary",
          !ready && !error && !working && "bg-muted-foreground",
        )}
      />
      {t(phaseKeys[phase])}
    </Badge>
  );
}
