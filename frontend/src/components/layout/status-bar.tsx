import { CloudOff, Server } from "lucide-react";
import { useI18n } from "@/i18n";
import { isBusyPhase, phaseKeys } from "@/lib/phase";
import { cn } from "@/lib/utils";
import type { SessionState } from "@/types";

export function StatusBar({
  phase,
  clusterName,
  contextName,
  message,
}: {
  phase: SessionState["phase"];
  clusterName?: string;
  contextName: string;
  message?: string;
}) {
  const { t } = useI18n();
  const ready = phase === "connected";
  const working = isBusyPhase(phase);
  const error = phase === "error";
  const cluster = clusterName || contextName;
  const detail = cluster || t("statusbar.noCluster");

  return (
    <footer className="flex h-8 shrink-0 items-center justify-end gap-3 border-t border-border bg-muted/30 px-4 text-[11px]">
      <div className="flex min-w-0 items-center gap-2 text-muted-foreground">
        {cluster ? (
          <Server size={12} className="shrink-0" strokeWidth={1.9} />
        ) : (
          <CloudOff size={12} className="shrink-0" strokeWidth={1.9} />
        )}
        <span className="truncate font-medium text-foreground/80">{detail}</span>
        {contextName && clusterName && contextName !== clusterName && (
          <span className="hidden truncate text-muted-foreground sm:inline">
            {t("statusbar.context", { name: contextName })}
          </span>
        )}
      </div>

      <div className="flex shrink-0 items-center gap-2">
        {error && message ? (
          <span className="max-w-[240px] truncate text-destructive" title={message}>
            {message}
          </span>
        ) : null}
        <span
          className={cn(
            "inline-flex items-center gap-1.5 font-medium",
            ready && "text-success",
            error && "text-destructive",
            working && "text-primary",
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
        </span>
      </div>
    </footer>
  );
}
