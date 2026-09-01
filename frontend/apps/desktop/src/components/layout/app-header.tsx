import { Settings2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { WindowControls } from "@/components/layout/window-controls";
import { navKeys, type AppView } from "@/components/layout/navigation";
import { SidebarTrigger, useSidebar } from "@/components/ui/sidebar";
import { useI18n, type TranslationKey } from "@/i18n";

const headerKeys: Record<AppView, TranslationKey> = {
  overview: "header.overview",
  clusters: "header.clusters",
  workload: "header.workload",
  network: "header.network",
  sessions: "header.sessions",
  "host-aliases": "header.hostAliases",
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
    <header className="window-drag flex h-16 shrink-0 items-center justify-between border-b border-border/60 bg-background/80 px-6 backdrop-blur-md">
      <div className="flex min-w-0 items-center gap-4">
        <SidebarTrigger
          className="window-no-drag shrink-0 rounded-lg border border-border/60 bg-card/60 shadow-xs hover:bg-accent hover:border-border"
          aria-label={sidebarLabel}
          title={sidebarLabel}
        />
        <div className="min-w-0">
          <h1 className="truncate text-base font-semibold tracking-tight">{t(navKeys[view])}</h1>
          <p className="mt-0.5 truncate text-[11px] text-muted-foreground">
            {t(headerKeys[view])}
          </p>
        </div>
      </div>
      <div className="window-no-drag flex items-center gap-2">
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="rounded-lg hover:bg-accent"
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
