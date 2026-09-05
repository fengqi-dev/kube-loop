import { useEffect, useState } from "react";
import { Bot, Boxes, ChevronRight, Circle, Gauge, Globe, Layers, LogIn, LogOut, Network, Search, Server, Settings2, type LucideIcon } from "lucide-react";
import { hasExplorer, navKeys, type AppView } from "./navigation";
import { useI18n } from "@/i18n";
import type { ServerConnection } from "@/components/server/server-access-view";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";

export const navigation: Array<{ id: AppView; icon: LucideIcon }> = [
  { id: "overview", icon: Gauge }, { id: "clusters", icon: Server },
  { id: "workload", icon: Boxes }, { id: "network", icon: Network },
  { id: "sessions", icon: Layers }, { id: "host-aliases", icon: Globe }, { id: "mcp", icon: Bot },
];
export function AppSidebar({ view, connection, open, onNavigate, onDismiss, onResourceHost }: {
  view: AppView; connection: ServerConnection; open: boolean;
  onNavigate(view: AppView): void; onDismiss(): void; onResourceHost(node: HTMLDivElement | null): void;
}) {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  useEffect(() => setQuery(""), [view]);
  const [accountOpen, setAccountOpen] = useState(false);
  const { profileState, profile, inventory, auth, busy } = connection;
  const matches = (name: string) => name.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase());
  function navigate(next: AppView) { onNavigate(next); onDismiss(); }
  function railButton(id: AppView, Icon: LucideIcon) {
    return <Tooltip key={id}><TooltipTrigger asChild>
      <button className="activity-button" aria-label={t(navKeys[id])} aria-current={view === id ? "page" : undefined} onClick={() => navigate(id)}>
        <Icon size={18} strokeWidth={1.6} />
      </button>
    </TooltipTrigger><TooltipContent side="right">{t(navKeys[id])}</TooltipContent></Tooltip>;
  }
  return <>
    <nav className="activity-rail" aria-label={t("shell.navigation")}>
      <div className="activity-primary">{navigation.map(({ id, icon }) => railButton(id, icon))}</div>
      <div className="activity-bottom">
        <div className="relative">
          <button className="activity-button" aria-label={auth.authenticated ? auth.userName || t("workspace.identity") : t("shell.signIn")}
            aria-expanded={accountOpen} onClick={() => setAccountOpen(value => !value)}>
            {auth.authenticated ? <span className="account-avatar">{(auth.userName || "U").slice(0, 1).toUpperCase()}</span> : <LogIn size={17} />}
          </button>
          {accountOpen && <div className="account-popover">
            <p>{auth.userName || t("shell.signIn")}</p>
            <button disabled={Boolean(busy)} onClick={() => { setAccountOpen(false); if (auth.authenticated) void connection.logout(); else navigate("overview"); }}>
              {auth.authenticated ? <LogOut size={14} /> : <LogIn size={14} />}{t(auth.authenticated ? "shell.signOut" : "shell.signIn")}
            </button>
          </div>}
        </div>
        {railButton("settings", Settings2)}
      </div>
    </nav>
    {open && hasExplorer(view) && <>
      <button className="explorer-backdrop" aria-label={t("nav.collapseSidebar")} onClick={onDismiss} />
      <aside className="explorer-panel" aria-label={t("shell.explorer")}>
        <div className="explorer-caption">{t(navKeys[view])}</div>
        <div className="explorer-content">
          {["overview", "clusters"].includes(view) && <details open className="explorer-group">
            <summary>{t("nav.clusters")}<ChevronRight size={13} /></summary>
            {profileState.profiles.filter(item => matches(item.displayName || item.id)).map(item =>
              <button className="explorer-row" data-selected={item.id === profileState.activeProfileId} key={item.id}
                disabled={Boolean(busy)} title={item.baseUrl} onClick={() => { void connection.selectProfile(item.id); onDismiss(); }}>
                <Server size={14} /><span>{item.displayName || item.id}</span>{item.id === profile?.id && <Circle size={6} fill="currentColor" className="text-primary" />}
              </button>)}
            <button className="explorer-row explorer-secondary" onClick={() => navigate("clusters")}><span className="ml-5">{t("shell.manageServers")}</span></button>
          </details>}
          {["workload", "network", "sessions"].includes(view) && <details open className="explorer-group">
            <summary>{t("network.colNamespace")}<ChevronRight size={13} /></summary>
            {(inventory?.namespaces ?? []).filter(item => matches(item.name)).map(item => <button key={item.name} className="explorer-row" data-selected={inventory?.namespace === item.name}
              disabled={Boolean(busy)} onClick={() => { void connection.loadInventory(item.name); onDismiss(); }}><Layers size={14} /><span>{item.name}</span></button>)}
            {!inventory?.namespaces.length && <p className="explorer-hint">{t("shell.noNamespace")}</p>}
          </details>}
          <div ref={onResourceHost} />
          {view === "settings" && (["theme", "language", "logLevel", "networkRuntimeTitle", "updateTitle"] as const).map(section => {
            const label = t(`settings.${section}`);
            return matches(label) && <button key={section} className="explorer-row" onClick={() => {
              document.getElementById(`settings-${section}`)?.scrollIntoView({ block: "start" });
              onDismiss();
            }}><Settings2 size={14} /><span>{label}</span></button>;
          })}
        </div>
        <label className="explorer-search"><Search size={13} /><input aria-label={t("shell.searchExplorer")} placeholder={t(["workload", "network", "sessions"].includes(view) ? "network.colNamespace" : "shell.searchExplorer")} value={query} onChange={event => setQuery(event.target.value)} /></label>
      </aside>
    </>}
  </>;
}
