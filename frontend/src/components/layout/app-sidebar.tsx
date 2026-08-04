import { useEffect, useState, type ReactNode } from "react";
import {
  Bot,
  Boxes,
  Gauge,
  Network,
  PanelLeftClose,
  PanelLeftOpen,
  ScrollText,
  Server,
  Settings2,
  Globe,
  Waypoints,
  type LucideIcon,
} from "lucide-react";
import { LogoMark } from "@/components/brand/logo-mark";
import { TrafficStats } from "@/components/overview/traffic-stats";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { type AppView } from "@/hooks/use-session";
import { useI18n, type TranslationKey } from "@/i18n";
import { cn } from "@/lib/utils";
import type { Metrics } from "@/types";

const navigation: Array<{ id: Exclude<AppView, "settings">; icon: LucideIcon }> = [
  { id: "overview", icon: Gauge },
  { id: "clusters", icon: Server },
  { id: "connections", icon: Waypoints },
  { id: "workload", icon: Boxes },
  { id: "network", icon: Network },
  { id: "host-aliases", icon: Globe },
  { id: "mcp", icon: Bot },
  { id: "logs", icon: ScrollText },
];

const navKeys: Record<AppView, TranslationKey> = {
  overview: "nav.overview",
  clusters: "nav.clusters",
  connections: "nav.connections",
  workload: "nav.workload",
  network: "nav.network",
  "host-aliases": "nav.hostAliases",
  logs: "nav.logs",
  mcp: "nav.mcp",
  settings: "nav.settings",
};

const storageKey = "kubeloop.sidebar.collapsed";

export function AppSidebar({
  view,
  connectionsAlert,
  updateAvailable,
  ready,
  metrics,
  onNavigate,
}: {
  view: AppView;
  /** Green dot on Connections: true when unread/new sessions appeared. */
  connectionsAlert?: boolean;
  updateAvailable: boolean;
  ready: boolean;
  metrics?: Metrics;
  onNavigate(view: AppView): void;
}) {
  const { t } = useI18n();
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return localStorage.getItem(storageKey) === "1";
    } catch {
      return false;
    }
  });

  useEffect(() => {
    try {
      localStorage.setItem(storageKey, collapsed ? "1" : "0");
    } catch {
      // ignore quota / private mode
    }
  }, [collapsed]);

  function navButton({
    id,
    Icon,
    label,
    trailing,
    dot,
  }: {
    id: AppView;
    Icon: LucideIcon;
    label: string;
    trailing?: ReactNode;
    dot?: "success" | "primary";
  }) {
    const active = view === id;
    const button = (
      <Button
        type="button"
        variant="ghost"
        onClick={() => onNavigate(id)}
        aria-label={label}
        className={cn(
          "h-9 rounded-md text-[14px] font-medium",
          collapsed
            ? "w-9 justify-center px-0"
            : "w-full justify-start gap-2.5 px-2.5",
          active
            ? "bg-sidebar-accent text-sidebar-accent-foreground hover:bg-sidebar-accent"
            : "text-muted-foreground hover:bg-accent/70 hover:text-foreground",
        )}
      >
        <Icon
          size={16}
          strokeWidth={1.9}
          className={cn(
            active ? "text-foreground" : "text-muted-foreground",
            id === "settings" && active && "text-primary",
          )}
        />
        {!collapsed ? (
          <>
            {label}
            {trailing}
          </>
        ) : null}
      </Button>
    );

    if (!collapsed) return button;

    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="relative inline-flex">
            {button}
            {dot ? (
              <span
                className={cn(
                  "pointer-events-none absolute top-1.5 right-1.5 size-1.5 rounded-full",
                  dot === "success" ? "bg-success" : "bg-primary",
                )}
              />
            ) : null}
          </span>
        </TooltipTrigger>
        <TooltipContent side="right" sideOffset={8}>
          {label}
        </TooltipContent>
      </Tooltip>
    );
  }

  const collapseLabel = collapsed ? t("nav.expandSidebar") : t("nav.collapseSidebar");
  const CollapseIcon = collapsed ? PanelLeftOpen : PanelLeftClose;

  return (
    <aside
      className={cn(
        "relative z-10 flex shrink-0 flex-col border-r border-sidebar-border bg-sidebar py-3 text-sidebar-foreground transition-[width] duration-200",
        collapsed ? "w-[60px] px-2" : "w-[220px] px-3",
      )}
    >
      <div
        className={cn(
          "window-drag flex items-center",
          collapsed ? "flex-col gap-2" : "h-11 gap-2.5 px-2",
        )}
      >
        <LogoMark className="size-9 shrink-0" />
        {!collapsed ? (
          <div className="min-w-0 flex-1">
            <div className="truncate text-sm font-semibold tracking-tight">KubeLoop</div>
            <div className="truncate text-[11px] text-muted-foreground">Network client</div>
          </div>
        ) : null}
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              aria-label={collapseLabel}
              onClick={() => setCollapsed((value) => !value)}
              className="window-no-drag size-8 shrink-0 px-0 text-muted-foreground hover:bg-accent/70 hover:text-foreground"
            >
              <CollapseIcon size={16} strokeWidth={1.9} />
            </Button>
          </TooltipTrigger>
          <TooltipContent side={collapsed ? "right" : "bottom"} sideOffset={8}>
            {collapseLabel}
          </TooltipContent>
        </Tooltip>
      </div>

      <nav
        className={cn(
          "mt-5 min-h-0 flex-1 space-y-1 overflow-y-auto",
          collapsed && "flex flex-col items-center",
        )}
      >
        {navigation.map(({ id, icon }) =>
          navButton({
            id,
            Icon: icon,
            label: t(navKeys[id]),
            trailing:
              id === "connections" && connectionsAlert ? (
                <span className="ml-auto size-1.5 rounded-full bg-success" />
              ) : undefined,
            dot: id === "connections" && connectionsAlert ? "success" : undefined,
          }),
        )}
      </nav>

      <div
        className={cn(
          "mt-auto space-y-2 pb-1",
          collapsed ? "flex flex-col items-center px-0" : "px-1",
        )}
      >
        {!collapsed ? <TrafficStats ready={ready} metrics={metrics} /> : null}
        <Separator className="bg-sidebar-border" />
        {navButton({
          id: "settings",
          Icon: Settings2,
          label: t("nav.settings"),
          trailing: updateAvailable ? (
            <span className="ml-auto size-1.5 rounded-full bg-primary" />
          ) : undefined,
          dot: updateAvailable ? "primary" : undefined,
        })}
      </div>
    </aside>
  );
}

export { navKeys };
