import { backend } from "@/backend";
import { ConnectionsView } from "@/components/connections/connections-view";
import { HostAliasesView } from "@/components/host-aliases/host-aliases-view";
import { AppHeader } from "@/components/layout/app-header";
import { AppSidebar } from "@/components/layout/app-sidebar";
import type { AppView } from "@/components/layout/navigation";
import { MCPView } from "@/components/mcp/mcp-view";
import { WindowControls } from "@/components/layout/window-controls";
import { ServerAccessView } from "@/components/server/server-access-view";
import { ServerLogsView } from "@/components/server/server-logs-view";
import { ServerNetworkView } from "@/components/server/server-network-view";
import { ServerWorkloadView } from "@/components/server/server-workload-view";
import { SettingsView } from "@/components/settings/settings-view";
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar";
import { Spinner } from "@/components/ui/spinner";
import type { AuthSession, BootstrapData, Metrics, ServerProfileState } from "@/types";
import { toast } from "sonner";
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";

function App() {
  const [data, setData] = useState<BootstrapData>();
  const [profiles, setProfiles] = useState<ServerProfileState>();
  const [auth, setAuth] = useState<AuthSession>({ authenticated: false });
  const [view, setView] = useState<AppView>("overview");
  const [metrics, setMetrics] = useState<Metrics>();
  const [metricsError, setMetricsError] = useState("");
  const [updateBusy, setUpdateBusy] = useState(false);
  const [logoutBusy, setLogoutBusy] = useState(false);
  const [error, setError] = useState("");
  const authQueryGeneration = useRef(0);
  const [sidebarOpen, setSidebarOpen] = useState(() => {
    try {
      return localStorage.getItem("kubeloop.sidebar.collapsed") !== "1";
    } catch {
      return true;
    }
  });

  useEffect(() => {
    let active = true;
    backend
      .bootstrap()
      .then((result) => {
        if (active) {
          setData(result);
          setProfiles(result.serverProfiles);
        }
      })
      .catch((reason: unknown) => {
        if (active) setError(reason instanceof Error ? reason.message : String(reason));
      });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    try {
      localStorage.setItem("kubeloop.sidebar.collapsed", sidebarOpen ? "0" : "1");
    } catch {
      // Ignore unavailable storage.
    }
  }, [sidebarOpen]);

  const activeProfile = useMemo(
    () => profiles?.profiles.find((item) => item.id === profiles.activeProfileId),
    [profiles],
  );

  const updateAuth = useCallback((session: AuthSession) => {
    authQueryGeneration.current += 1;
    setAuth(session);
  }, []);

  useEffect(() => {
    if (!activeProfile) {
      setMetrics(undefined);
      setMetricsError("");
      return;
    }
    let active = true;
    const refresh = async () => {
      try {
        const next = await backend.serverDataPlaneMetrics(activeProfile.id);
        if (active) {
          setMetrics(next);
          setMetricsError("");
        }
      } catch (reason) {
        if (active) {
          setMetrics(undefined);
          setMetricsError(reason instanceof Error ? reason.message : String(reason));
        }
      }
    };
    void refresh();
    const timer = window.setInterval(() => void refresh(), 1_000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [activeProfile]);

  useEffect(() => {
    if (!activeProfile) {
      updateAuth({ authenticated: false });
      return;
    }
    const generation = ++authQueryGeneration.current;
    let active = true;
    backend.serverAuthStatus(activeProfile.id)
      .then((session) => {
        if (active && generation === authQueryGeneration.current) setAuth(session);
      })
      .catch(() => {
        if (active && generation === authQueryGeneration.current) setAuth({ authenticated: false });
      });
    return () => { active = false; };
  }, [activeProfile, updateAuth]);

  async function checkForUpdates() {
    if (!data || updateBusy) return;
    setUpdateBusy(true);
    try {
      const update = await backend.checkForUpdates();
      setData({ ...data, update });
    } finally {
      setUpdateBusy(false);
    }
  }

  async function logout() {
    if (!activeProfile || logoutBusy) return;
    setLogoutBusy(true);
    try {
      await backend.logoutServer(activeProfile.id);
      updateAuth({ authenticated: false });
      setView("overview");
    } catch (reason) {
      toast.error("Sign out failed", { description: reason instanceof Error ? reason.message : String(reason) });
    } finally {
      setLogoutBusy(false);
    }
  }

  if (error) return (
    <div className="grid min-h-screen place-items-center bg-background px-8 text-center text-sm text-destructive">
      {error}
      <div className="window-drag fixed inset-x-0 top-0 z-50 flex h-14 items-center justify-end px-3">
        <WindowControls />
      </div>
    </div>
  );
  if (!data || !profiles) return (
    <div className="grid min-h-screen place-items-center bg-background text-foreground">
      <Spinner />
      <div className="window-drag fixed inset-x-0 top-0 z-50 flex h-14 items-center justify-end px-3">
        <WindowControls />
      </div>
    </div>
  );

  return (
    <SidebarProvider
      open={sidebarOpen}
      onOpenChange={setSidebarOpen}
      className="h-screen min-h-[580px] overflow-hidden bg-background text-foreground"
      style={{ "--sidebar-width": "176px", "--sidebar-width-icon": "60px" } as CSSProperties}
    >
      <AppSidebar
        view={view}
        connectionsAlert={Boolean(metrics?.connections?.length)}
        updateAvailable={data.update.available}
        authenticated={auth.authenticated}
        userName={auth.userName || activeProfile?.lastUserName}
        logoutBusy={logoutBusy}
        onNavigate={setView}
        onSignIn={() => setView("overview")}
        onLogout={() => void logout()}
      />
      <SidebarInset className="min-w-0 overflow-hidden">
        <AppHeader view={view} onOpenSettings={() => setView("settings")} />
        <div className={`min-h-0 flex-1 overflow-y-auto px-6 py-5 ${view === "overview" ? "scrollbar-none !py-3" : ""}`}>
          {view === "connections" ? (
            <div className="space-y-3">
              {metricsError ? <p className="text-sm text-muted-foreground">{metricsError}</p> : null}
              <ConnectionsView ready={Boolean(metrics)} metrics={metrics} />
            </div>
          ) : view === "host-aliases" ? (
            <HostAliasesView
              profileId={activeProfile?.id ?? ""}
              profileName={activeProfile?.displayName}
              ready={Boolean(metrics)}
            />
          ) : view === "logs" ? (
            <ServerLogsView profileId={activeProfile?.id ?? ""} />
          ) : view === "workload" ? (
            <ServerWorkloadView profileId={activeProfile?.id ?? ""} />
          ) : view === "network" ? (
            <ServerNetworkView profileId={activeProfile?.id ?? ""} />
          ) : view === "mcp" ? (
            <MCPView />
          ) : view === "settings" ? (
            <SettingsView
              profileId={activeProfile?.id ?? ""}
              ready={Boolean(metrics)}
              update={data.update}
              checking={updateBusy}
              onCheck={() => void checkForUpdates()}
              onOpen={() => void backend.openUpdatePage()}
            />
          ) : view === "clusters" ? (
            <ServerAccessView
              profiles={profiles}
              authSession={auth}
              management
              onProfilesChange={setProfiles}
              onAuthChange={updateAuth}
              onNavigate={setView}
            />
          ) : (
            <ServerAccessView
              profiles={profiles}
              authSession={auth}
              onProfilesChange={setProfiles}
              onAuthChange={updateAuth}
              onNavigate={setView}
            />
          )}
        </div>
      </SidebarInset>
    </SidebarProvider>
  );
}

export default App;
