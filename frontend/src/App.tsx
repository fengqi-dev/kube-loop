import { AppHeader } from "@/components/layout/app-header";
import { AppSidebar } from "@/components/layout/app-sidebar";
import { StatusBar } from "@/components/layout/status-bar";
import { ClustersView } from "@/components/clusters/clusters-view";
import { ConnectionsView } from "@/components/connections/connections-view";
import { HostAliasesView } from "@/components/host-aliases/host-aliases-view";
import { LogsView } from "@/components/logs/logs-view";
import { MCPView } from "@/components/mcp/mcp-view";
import { NetworkView } from "@/components/network/network-view";
import { WorkloadView } from "@/components/network/workload-view";
import { OverviewView } from "@/components/overview/overview-view";
import { SettingsView } from "@/components/settings/settings-view";
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar";
import { useSession } from "@/hooks/use-session";
import { cn } from "@/lib/utils";
import { useEffect, useMemo, useRef, useState, type CSSProperties } from "react";

function App() {
  const {
    data,
    contextName,
    connectionMode,
    activeContextName,
    activeNamespaces,
    view,
    setView,
    loading,
    uiError,
    updateBusy,
    session,
    busy,
    ready,
    currentContext,
    kubeconfigFiles,
    changeContext,
    setConnectionMode,
    toggleConnection,
    connectContext,
    reloadContexts,
    addKubeconfig,
    removeKubeconfig,
    probeContext,
    checkForUpdates,
    openUpdatePage,
  } = useSession();

  const scopedNamespaces = session.scopeNamespaces ?? [];
  const inventoryNamespaces = useMemo(
    () => (scopedNamespaces.length > 0 ? scopedNamespaces : activeNamespaces),
    [activeNamespaces, scopedNamespaces],
  );
  const namespaceScoped = scopedNamespaces.length > 0;

  const [connectionsAlert, setConnectionsAlert] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(() => {
    try {
      return localStorage.getItem("kubeloop.sidebar.collapsed") !== "1";
    } catch {
      return true;
    }
  });
  const seenConnectionIds = useRef(new Set<string>());

  useEffect(() => {
    try {
      localStorage.setItem("kubeloop.sidebar.collapsed", sidebarOpen ? "0" : "1");
    } catch {
      // Ignore unavailable storage.
    }
  }, [sidebarOpen]);

  useEffect(() => {
    if (!ready) {
      seenConnectionIds.current.clear();
      setConnectionsAlert(false);
      return;
    }
    const ids = (session.metrics?.connections ?? [])
      .map((item) => item.id)
      .filter((id): id is string => Boolean(id));
    if (view === "connections") {
      for (const id of ids) seenConnectionIds.current.add(id);
      setConnectionsAlert(false);
      return;
    }
    if (ids.some((id) => !seenConnectionIds.current.has(id))) {
      setConnectionsAlert(true);
    }
  }, [ready, session.metrics?.connections, view]);

  return (
    <SidebarProvider
      open={sidebarOpen}
      onOpenChange={setSidebarOpen}
      className="h-screen min-h-[580px] overflow-hidden bg-background text-foreground"
      style={{
        "--sidebar-width": "176px",
        "--sidebar-width-icon": "60px",
      } as CSSProperties}
    >
      <AppSidebar
        view={view}
        connectionsAlert={connectionsAlert}
        updateAvailable={data.update.available}
        onNavigate={setView}
      />

      <SidebarInset className="min-w-0 overflow-hidden">
        <AppHeader
          view={view}
          onOpenSettings={() => setView("settings")}
        />

        <div
          className={cn(
            "min-h-0 flex-1 overflow-y-auto px-6 py-5",
            view === "overview" && "scrollbar-none",
          )}
        >
          {view === "overview" && (
            <OverviewView
              contextName={activeContextName}
              clusterName={currentContext?.cluster ?? ""}
              session={session}
              connectionMode={connectionMode}
              platform={data.platform}
              loading={loading}
              error={uiError || session.error || ""}
              busy={busy}
              ready={ready}
              onToggle={() => void toggleConnection()}
              onConnectionModeChange={setConnectionMode}
              onManageClusters={() => setView("clusters")}
              onNavigate={setView}
            />
          )}
          {view === "clusters" && (
            <ClustersView
              contexts={data.contexts}
              files={kubeconfigFiles}
              contextName={contextName}
              session={session}
              busy={busy}
              ready={ready}
              loading={loading}
              onSelect={(value) => void changeContext(value)}
              onConnect={(value) => void connectContext(value)}
              onReload={reloadContexts}
              onAddFile={addKubeconfig}
              onRemoveFile={removeKubeconfig}
              onProbe={probeContext}
            />
          )}
          {view === "connections" && (
            <ConnectionsView ready={ready} metrics={session.metrics} />
          )}
          {view === "workload" && (
            <WorkloadView
              contextName={activeContextName}
              namespaces={inventoryNamespaces}
              namespaceScoped={namespaceScoped}
              ready={ready}
              session={session}
            />
          )}
          {view === "network" && (
            <NetworkView
              contextName={activeContextName}
              namespaces={inventoryNamespaces}
              namespaceScoped={namespaceScoped}
              ready={ready}
              session={session}
            />
          )}
          {view === "host-aliases" && (
            <HostAliasesView contextName={contextName} ready={ready} />
          )}
          {view === "logs" && (
            <LogsView session={session} error={uiError || session.error || ""} />
          )}
          {view === "mcp" && <MCPView />}
          {view === "settings" && (
            <SettingsView
              ready={ready}
              coreVersion={session.coreVersion}
              update={data.update}
              checking={updateBusy}
              onCheck={() => void checkForUpdates()}
              onOpen={() => void openUpdatePage()}
            />
          )}
        </div>

        <StatusBar
          phase={session.phase}
          clusterName={currentContext?.cluster || session.context}
          contextName={session.context || contextName}
          message={uiError || session.error || session.message}
        />
      </SidebarInset>
    </SidebarProvider>
  );
}

export default App;
