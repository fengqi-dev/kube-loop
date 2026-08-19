import {
  Bot,
  Boxes,
  ChevronsUpDown,
  Gauge,
  Globe,
  LogIn,
  LogOut,
  Network,
  Server,
  Settings2,
  ScrollText,
  Waypoints,
  type LucideIcon,
} from "lucide-react";
import { LogoMark } from "@/components/brand/logo-mark";
import { navKeys, type AppView } from "@/components/layout/navigation";
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
import { useI18n } from "@/i18n";
import { cn } from "@/lib/utils";
import { useEffect, useRef, useState } from "react";

const navigation: Array<{ id: Exclude<AppView, "settings">; icon: LucideIcon }> = [
  { id: "overview", icon: Gauge },
  { id: "clusters", icon: Server },
  { id: "connections", icon: Waypoints },
  { id: "workload", icon: Boxes },
  { id: "network", icon: Network },
  { id: "host-aliases", icon: Globe },
  { id: "traffic-inspection", icon: ScrollText },
  { id: "mcp", icon: Bot },
];

export function AppSidebar({
  view,
  connectionsAlert,
  updateAvailable,
  authenticated,
  userName,
  logoutBusy,
  onNavigate,
  onSignIn,
  onLogout,
}: {
  view: AppView;
  connectionsAlert?: boolean;
  updateAvailable: boolean;
  authenticated?: boolean;
  userName?: string;
  logoutBusy?: boolean;
  onNavigate(view: AppView): void;
  onSignIn(): void;
  onLogout(): void;
}) {
  const { t } = useI18n();
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const userMenuRef = useRef<HTMLLIElement>(null);

  useEffect(() => {
    if (!userMenuOpen) return;
    function closeOnPointerDown(event: PointerEvent) {
      if (!userMenuRef.current?.contains(event.target as Node)) setUserMenuOpen(false);
    }
    function closeOnEscape(event: KeyboardEvent) {
      if (event.key === "Escape") setUserMenuOpen(false);
    }
    document.addEventListener("pointerdown", closeOnPointerDown);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOnPointerDown);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [userMenuOpen]);

  function menuItem(
    id: AppView,
    Icon: LucideIcon,
    indicator?: "success" | "primary",
  ) {
    const label = t(navKeys[id]);
    const active = view === id;
    return (
      <SidebarMenuItem key={id}>
        <SidebarMenuButton
          type="button"
          size="default"
          isActive={active}
          tooltip={{ children: label, sideOffset: 8 }}
          aria-label={label}
          onClick={() => onNavigate(id)}
          className={cn(
            "relative h-9 gap-2.5 rounded-xl text-[13px] font-medium transition-all duration-200 group-data-[collapsible=icon]:size-10! group-data-[collapsible=icon]:p-2.5!",
            active
              ? "bg-sidebar-primary/10 text-sidebar-primary font-semibold"
              : "text-sidebar-foreground/70 hover:text-sidebar-foreground hover:bg-sidebar-accent/50",
          )}
        >
          {active && (
            <span className="absolute right-1.5 top-1/2 h-5 w-1 -translate-y-1/2 rounded-full bg-sidebar-primary group-data-[collapsible=icon]:hidden" />
          )}
          <Icon
            strokeWidth={active ? 2.2 : 1.8}
            className={cn(
              "size-[18px]! transition-colors duration-200 group-data-[collapsible=icon]:size-5!",
              active ? "text-sidebar-primary" : "text-sidebar-foreground/60",
            )}
          />
          <span>{label}</span>
          {indicator ? (
            <span
              className={cn(
                "ml-auto size-1.5 shrink-0 rounded-full group-data-[collapsible=icon]:absolute group-data-[collapsible=icon]:top-1.5 group-data-[collapsible=icon]:right-1.5",
                indicator === "success" ? "bg-success" : "bg-sidebar-primary",
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
      className="z-10 border-r border-sidebar-border bg-sidebar/80 backdrop-blur-xl [--sidebar-width-icon:56px]"
    >
      <SidebarHeader className="window-drag px-3 pt-4 pb-2">
        <div className="flex h-11 items-center gap-2.5">
          <div className="flex size-8 shrink-0 items-center justify-center rounded-xl bg-gradient-to-br from-sidebar-primary to-sidebar-primary/80 text-sidebar-primary-foreground shadow-lg shadow-sidebar-primary/20 group-data-[collapsible=icon]:size-7">
            <LogoMark className="size-5 group-data-[collapsible=icon]:size-4" />
          </div>
          <div className="min-w-0 flex-1 group-data-[collapsible=icon]:hidden">
            <div className="truncate text-[13px] font-bold tracking-tight text-sidebar-foreground">KubeLoop</div>
            <div className="truncate text-[10px] text-sidebar-foreground/50">Network Client</div>
          </div>
        </div>
      </SidebarHeader>

      <SidebarContent className="mt-3">
        <SidebarGroup className="px-2 py-0">
          <SidebarGroupContent>
            <SidebarMenu className="gap-0.5">
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

      <SidebarFooter className="gap-1.5 p-2 group-data-[collapsible=icon]:px-2">
        <SidebarSeparator className="mx-0 bg-sidebar-border" />
        <SidebarMenu>
          <SidebarMenuItem ref={userMenuRef}>
            <SidebarMenuButton
              type="button"
              size="default"
              aria-label={authenticated ? userName || "Authenticated user" : "Sign in"}
              aria-haspopup="menu"
              aria-expanded={userMenuOpen}
              data-state={userMenuOpen ? "open" : "closed"}
              onClick={() => setUserMenuOpen((open) => !open)}
              className="h-9 gap-2 rounded-xl text-sidebar-foreground/70 data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground hover:bg-sidebar-accent/50 group-data-[collapsible=icon]:size-10! group-data-[collapsible=icon]:p-2!"
            >
              <span className={cn(
                "grid size-6 shrink-0 place-items-center rounded-full text-[9px] font-semibold transition-colors",
                authenticated ? "bg-success text-success-foreground" : "bg-sidebar-accent text-sidebar-foreground/60",
              )}>
                {authenticated ? initials(userName) : <LogIn size={14} />}
              </span>
              <span className="min-w-0 flex-1 text-left group-data-[collapsible=icon]:hidden">
                <span className="block truncate text-[12px] font-medium">
                  {authenticated ? userName || "Authenticated user" : "Sign in"}
                </span>
              </span>
              <ChevronsUpDown size={14} className="ml-auto text-sidebar-foreground/40 group-data-[collapsible=icon]:hidden" />
              {updateAvailable ? <span className="absolute top-1 right-1 size-1.5 rounded-full bg-sidebar-primary" /> : null}
            </SidebarMenuButton>
            {userMenuOpen ? (
              <div role="menu" aria-label="User menu" className="absolute bottom-[calc(100%+0.5rem)] left-0 z-[100] w-full min-w-[180px] rounded-xl bg-popover p-1.5 text-popover-foreground shadow-xl ring-1 ring-foreground/10 backdrop-blur-md">
                <div className="flex items-center gap-2 p-2">
                  <span className={cn(
                    "grid size-7 shrink-0 place-items-center rounded-full text-[10px] font-semibold",
                    authenticated ? "bg-success text-success-foreground" : "bg-muted text-muted-foreground",
                  )}>
                    {authenticated ? initials(userName) : <LogIn size={14} />}
                  </span>
                  <span className="truncate text-[12px] font-medium">
                    {authenticated ? userName || "Authenticated user" : "Not signed in"}
                  </span>
                </div>
                <div className="-mx-1 my-1 h-px bg-border" />
                <button type="button" role="menuitem" className="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-[13px] hover:bg-accent hover:text-accent-foreground" onPointerDown={(event) => { event.preventDefault(); event.stopPropagation(); setUserMenuOpen(false); onNavigate("settings"); }} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); setUserMenuOpen(false); onNavigate("settings"); } }}>
                  <Settings2 size={16} />
                  {t("nav.settings")}
                  {updateAvailable ? <span className="ml-auto size-1.5 rounded-full bg-primary" /> : null}
                </button>
                <div className="-mx-1 my-1 h-px bg-border" />
                {authenticated ? (
                  <button type="button" role="menuitem" disabled={logoutBusy} className="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-[13px] text-destructive hover:bg-destructive/10 disabled:pointer-events-none disabled:opacity-50" onPointerDown={(event) => { event.preventDefault(); event.stopPropagation(); setUserMenuOpen(false); onLogout(); }} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); setUserMenuOpen(false); onLogout(); } }}>
                    <LogOut size={16} />
                    {logoutBusy ? "Logging out…" : "Log out"}
                  </button>
                ) : (
                  <button type="button" role="menuitem" className="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-[13px] hover:bg-accent hover:text-accent-foreground" onPointerDown={(event) => { event.preventDefault(); event.stopPropagation(); setUserMenuOpen(false); onSignIn(); }} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); setUserMenuOpen(false); onSignIn(); } }}>
                    <LogIn size={16} /> Sign in
                  </button>
                )}
              </div>
            ) : null}
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
    </Sidebar>
  );
}

function initials(value?: string) {
  const parts = (value || "U").trim().split(/\s+/).filter(Boolean);
  return parts.slice(0, 2).map((part) => part[0]?.toUpperCase()).join("") || "U";
}
