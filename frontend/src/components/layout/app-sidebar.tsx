import {
  Bot,
  Boxes,
  Gauge,
  Globe,
  Network,
  ScrollText,
  Server,
  Settings2,
  Waypoints,
  type LucideIcon,
} from "lucide-react";
import { LogoMark } from "@/components/brand/logo-mark";
import { navKeys } from "@/components/layout/navigation";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarSeparator,
} from "@/components/ui/sidebar";
import { type AppView } from "@/hooks/use-session";
import { useI18n } from "@/i18n";
import { cn } from "@/lib/utils";

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

export function AppSidebar({
  view,
  connectionsAlert,
  updateAvailable,
  onNavigate,
}: {
  view: AppView;
  connectionsAlert?: boolean;
  updateAvailable: boolean;
  onNavigate(view: AppView): void;
}) {
  const { t } = useI18n();

  function menuItem(
    id: AppView,
    Icon: LucideIcon,
    indicator?: "success" | "primary",
  ) {
    const label = t(navKeys[id]);
    return (
      <SidebarMenuItem key={id}>
        <SidebarMenuButton
          type="button"
          size="default"
          isActive={view === id}
          tooltip={{ children: label, sideOffset: 8 }}
          aria-label={label}
          onClick={() => onNavigate(id)}
          className="h-9 gap-2.5 text-[14px] font-medium group-data-[collapsible=icon]:size-10! group-data-[collapsible=icon]:p-2.5!"
        >
          <Icon
            strokeWidth={1.9}
            className={cn(
              "size-[18px]! group-data-[collapsible=icon]:size-5!",
              view === id ? "text-foreground" : "text-muted-foreground",
              id === "settings" && view === id && "text-primary",
            )}
          />
          <span>{label}</span>
          {indicator ? (
            <span
              className={cn(
                "ml-auto size-1.5 shrink-0 rounded-full group-data-[collapsible=icon]:absolute group-data-[collapsible=icon]:top-1.5 group-data-[collapsible=icon]:right-1.5",
                indicator === "success" ? "bg-success" : "bg-primary",
              )}
            />
          ) : null}
        </SidebarMenuButton>
      </SidebarMenuItem>
    );
  }

  return (
    <Sidebar
      collapsible="icon"
      className="z-10 [--sidebar-width-icon:60px]"
    >
      <SidebarHeader className="window-drag px-3 pt-3 pb-1">
        <div className="flex h-11 items-center gap-2.5">
          <LogoMark className="size-9 shrink-0 group-data-[collapsible=icon]:size-8" />
          <div className="min-w-0 flex-1 group-data-[collapsible=icon]:hidden">
            <div className="truncate text-sm font-semibold tracking-tight">KubeLoop</div>
            <div className="truncate text-[11px] text-muted-foreground">Network client</div>
          </div>
        </div>
      </SidebarHeader>

      <SidebarContent className="mt-5">
        <SidebarGroup className="px-2 py-0">
          <SidebarGroupContent>
            <SidebarMenu className="gap-1">
              {navigation.map(({ id, icon }) =>
                menuItem(
                  id,
                  icon,
                  id === "connections" && connectionsAlert ? "success" : undefined,
                ),
              )}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>

      <SidebarFooter className="gap-2 p-3 group-data-[collapsible=icon]:px-2">
        <SidebarSeparator className="mx-0" />
        <SidebarMenu>
          {menuItem("settings", Settings2, updateAvailable ? "primary" : undefined)}
        </SidebarMenu>
      </SidebarFooter>
    </Sidebar>
  );
}
