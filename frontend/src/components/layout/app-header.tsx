import { Settings2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { WindowControls } from "@/components/layout/window-controls";
import { navKeys } from "@/components/layout/navigation";
import { SidebarTrigger, useSidebar } from "@/components/ui/sidebar";
import type { AppView } from "@/hooks/use-session";
import { useI18n, type TranslationKey } from "@/i18n";

const headerKeys: Record<AppView, TranslationKey> = {
  overview: "header.overview",
  clusters: "header.clusters",
  connections: "header.connections",
  workload: "header.workload",
  network: "header.network",
  "host-aliases": "header.hostAliases",
  logs: "header.logs",
  mcp: "header.mcp",
  settings: "header.settings",
};

export function AppHeader({
  view,
  onOpenSettings,
}: {
  view: AppView;
  onOpenSettings(): void;
}) {
  const { t } = useI18n();
  const { state } = useSidebar();
  const sidebarLabel =
    state === "collapsed" ? t("nav.expandSidebar") : t("nav.collapseSidebar");

  return (
    <header className="window-drag flex h-14 shrink-0 items-center justify-between border-b border-border bg-background px-6">
      <div className="flex min-w-0 items-center gap-3">
        <SidebarTrigger
          className="window-no-drag shrink-0 border border-border bg-background shadow-xs hover:bg-accent"
          aria-label={sidebarLabel}
          title={sidebarLabel}
        />
        <div className="min-w-0">
          <h1 className="truncate text-sm font-semibold tracking-tight">{t(navKeys[view])}</h1>
          <p className="mt-0.5 truncate text-[12px] text-muted-foreground">
            {t(headerKeys[view])}
          </p>
        </div>
      </div>
      <div className="window-no-drag flex items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="icon"
          aria-label={t("nav.settings")}
          onClick={onOpenSettings}
        >
          <Settings2 size={16} />
        </Button>
        <WindowControls />
      </div>
    </header>
  );
}
