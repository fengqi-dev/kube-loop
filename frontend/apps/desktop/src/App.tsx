import { useCallback, useEffect, useState } from "react";
import { ChevronDown, Moon, PanelLeft, Sun } from "lucide-react";
import { backend } from "@/backend";
import { useI18n } from "@/i18n";
import { useIsMobile } from "@/hooks/use-mobile";
import { useTheme } from "@/hooks/use-theme";
import { AppHeader } from "@/components/layout/app-header";
import { AppSidebar } from "@/components/layout/app-sidebar";
import { navKeys, type AppView } from "@/components/layout/navigation";
import { ServerAccessView, useServerConnection } from "@/components/server/server-access-view";
import { ServerNetworkView } from "@/components/server/server-network-view";
import { ServerWorkloadView } from "@/components/server/server-workload-view";
import { SessionsView } from "@/components/sessions/sessions-view";
import { HostAliasesView } from "@/components/host-aliases/host-aliases-view";
import { MCPView } from "@/components/mcp/mcp-view";
import { SettingsView } from "@/components/settings/settings-view";
import { ResourceExplorer } from "@/components/workspace/resource-workspace";
import { Spinner } from "@/components/ui/spinner";
import { TooltipProvider } from "@/components/ui/tooltip";
import type { BootstrapData } from "@/types";

function App() {
  const [data, setData] = useState<BootstrapData>();
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    void backend.bootstrap().then(value => { if (active) setData(value); }).catch(reason => { if (active) setError(String(reason)); });
    return () => { active = false; };
  }, []);
  if (!data) return <div className="desktop-shell"><AppHeader /><div className="shell-loading">{error || <Spinner />}</div></div>;
  return <DesktopWorkspace initialData={data} />;
}

function DesktopWorkspace({ initialData }: { initialData: BootstrapData }) {
  const { t } = useI18n();
  const { resolved, setPreference } = useTheme();
  const mobile = useIsMobile();
  const [data, setData] = useState(initialData);
  const [profiles, setProfiles] = useState(initialData.serverProfiles);
  const [view, setView] = useState<AppView>("network");
  const [checking, setChecking] = useState(false);
  const [resourceHost, setResourceHost] = useState<HTMLDivElement | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(() => {
    try { return localStorage.getItem("kubeloop.sidebar.collapsed") === "0"; } catch { return false; }
  });
  const navigate = useCallback((next: AppView) => {
    setView(next);
    setDrawerOpen(false);
  }, []);
  const connection = useServerConnection({ profiles, onProfilesChange: setProfiles, onNavigate: navigate, overviewVisible: view === "overview" });
  const { profile, inventory, auth, busy, dataPlaneError, error } = connection;
  const connected = inventory?.dataPlane?.state === "connected";
  const profileId = auth.authenticated ? profile?.id ?? "" : "";
  const namespace = inventory?.namespace;
  useEffect(() => {
    try { localStorage.setItem("kubeloop.sidebar.collapsed", sidebarOpen ? "0" : "1"); } catch { /* optional preference */ }
  }, [sidebarOpen]);
  async function checkForUpdates() {
    if (checking) return;
    setChecking(true);
    try { const update = await backend.checkForUpdates(); setData(current => ({ ...current, update })); }
    finally { setChecking(false); }
  }
  const statusKey = busy === "tunnel" || inventory?.dataPlane?.state === "reconnecting" ? "phase.starting-tunnel" : connected ? "phase.connected" : inventory?.dataPlane?.state === "error" ? "phase.error" : "phase.idle";
  return <TooltipProvider delayDuration={300}>
    <div className="desktop-shell">
      <AppHeader platform={data.platform} />
      <div className="desktop-body">
        <AppSidebar onResourceHost={setResourceHost} view={view} connection={connection} open={mobile ? drawerOpen : sidebarOpen} onNavigate={navigate} onDismiss={() => setDrawerOpen(false)} />
        <div className="workbench">
          {(error || dataPlaneError) && view !== "overview" && <div className="shell-error" role="alert">{error || dataPlaneError}<button onClick={() => navigate("overview")}>{t("nav.overview")}<ChevronDown size={12} /></button></div>}
          <main className="app-content" aria-label={t(navKeys[view])}>
            <div className={view === "overview" || view === "clusters" ? `connection-page utility-page ${view === "clusters" ? "server-management" : ""}` : "hidden"}>
              <ServerAccessView connection={connection} management={view === "clusters"} />
            </div>
            <ResourcePages key={`${profileId}:${auth.authenticated}`} view={view} profileId={profileId} namespace={namespace} onNamespaceChange={connection.loadInventory} resourceHost={resourceHost} onResourceSelect={() => setDrawerOpen(false)} />
            {view === "host-aliases" ? <div className="utility-page"><HostAliasesView profileId={profileId} profileName={profile?.displayName} ready={connected} /></div>
              : view === "mcp" ? <div className="utility-page"><MCPView /></div>
              : view === "settings" ? <div className="utility-page"><SettingsView profileId={profileId} ready={connected} coreVersion={data.coreVersion} update={data.update} checking={checking} onCheck={() => void checkForUpdates()} onOpen={() => void backend.openUpdatePage()} /></div> : null}
          </main>
        </div>
      </div>
      <footer className="app-statusbar">
        <button aria-label={t((mobile ? drawerOpen : sidebarOpen) ? "nav.collapseSidebar" : "nav.expandSidebar")} onClick={() => mobile ? setDrawerOpen(value => !value) : setSidebarOpen(value => !value)}><PanelLeft size={16} /></button>
        <span className="connection-dot" data-connected={connected} /><span>{t(statusKey)}</span>
        <span className="status-context">{profile?.displayName || t("statusbar.noCluster")}{namespace ? ` / ${namespace}` : ""}</span>
        <span className="status-version">KubeLoop · {data.update.currentVersion || data.coreVersion}</span>
        <button aria-label={t("settings.theme")} onClick={() => setPreference(resolved === "dark" ? "light" : "dark")}>{resolved === "dark" ? <Moon size={15} /> : <Sun size={15} />}</button>
      </footer>
    </div>
  </TooltipProvider>;
}

function ResourcePages({ view, profileId, namespace, onNamespaceChange, resourceHost, onResourceSelect }: {
  view: AppView; profileId: string; namespace?: string; onNamespaceChange(namespace: string): void; resourceHost: HTMLDivElement | null; onResourceSelect(): void;
}) {
  const [visited, setVisited] = useState<Set<AppView>>(() => new Set([view]));
  useEffect(() => { setVisited(current => current.has(view) ? current : new Set([...current, view])); }, [view]);
  return <>{(["workload", "network", "sessions"] as const).map(page => (visited.has(page) || page === view) &&
    <div key={page} className={page === view ? "resource-page" : "hidden"}>
      <ResourceExplorer.Provider value={page === view ? { host: resourceHost, onSelect: onResourceSelect } : null}>
        {page === "workload" ? <ServerWorkloadView active={page === view} profileId={profileId} selectedNamespace={namespace} onNamespaceChange={onNamespaceChange} />
          : page === "network" ? <ServerNetworkView active={page === view} profileId={profileId} selectedNamespace={namespace} onNamespaceChange={onNamespaceChange} />
          : <SessionsView active={page === view} profileId={profileId} selectedNamespace={namespace} onNamespaceChange={onNamespaceChange} />}
      </ResourceExplorer.Provider>
    </div>)}</>;
}

export default App;
