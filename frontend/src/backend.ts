import type {
  BootstrapData,
  ClusterInventory,
  ConnectionMode,
  ConnectivityTestResult,
  HelperStatus,
  HostAlias,
  InterceptInfo,
  InterceptMapping,
  ManualNetwork,
  MCPInstallResult,
  MCPStatus,
  PodInfo,
  PortForwardInfo,
  PortForwardRequest,
  PreviewInfo,
  PreviewRequest,
  ProbeResult,
  ServiceInfo,
  Metrics,
  SessionIntentCounts,
  SessionState,
  UpdateInfo,
} from "./types";

declare global {
  interface Window {
    go?: {
      app?: {
        App?: {
          Bootstrap(): Promise<BootstrapData>;
          ReloadContexts(): Promise<ClusterInventory>;
          AddKubeconfig(): Promise<ClusterInventory>;
          RemoveKubeconfig(path: string): Promise<ClusterInventory>;
          ProbeContext(contextName: string): Promise<ProbeResult>;
          RememberSelection(contextName: string, namespace: string): Promise<void>;
          Namespaces(contextName: string): Promise<string[]>;
          ListServices(contextName: string, namespace: string): Promise<ServiceInfo[]>;
          ListPods(contextName: string, namespace: string): Promise<PodInfo[]>;
          Connect(contextName: string, namespace: string): Promise<void>;
          ConnectMode(
            contextName: string,
            namespace: string,
            mode: ConnectionMode,
          ): Promise<void>;
          Disconnect(): Promise<void>;
          TestPortForward(id: string): Promise<ConnectivityTestResult>;
          GetManualNetwork(contextName: string): Promise<ManualNetwork>;
          SetManualNetwork(contextName: string, network: ManualNetwork): Promise<void>;
          SetDNSNamespace(contextName: string, namespace: string): Promise<void>;
          GetHostAliases(contextName: string): Promise<HostAlias[]>;
          SetHostAliases(contextName: string, items: HostAlias[]): Promise<void>;
          GatewayInstallManifest(): Promise<string>;
          StartIntercept(mapping: InterceptMapping): Promise<InterceptInfo>;
          StartMirror(mapping: InterceptMapping): Promise<InterceptInfo>;
          StopIntercept(id: string): Promise<void>;
          TestIntercept(id: string): Promise<ConnectivityTestResult>;
          ListIntercepts(): Promise<InterceptInfo[]>;
          ListMirrors(): Promise<InterceptInfo[]>;
          StartPreview(request: PreviewRequest): Promise<PreviewInfo>;
          StopPreview(id: string): Promise<void>;
          ListPreviews(): Promise<PreviewInfo[]>;
          StartPortForward(request: PortForwardRequest): Promise<PortForwardInfo>;
          StopPortForward(id: string): Promise<void>;
          ListPortForwards(): Promise<PortForwardInfo[]>;
          ResetSessions(): Promise<void>;
          SessionIntentCounts(): Promise<SessionIntentCounts>;
          CheckForUpdates(): Promise<UpdateInfo>;
          OpenUpdatePage(): Promise<void>;
          GetSingBoxConfig(): Promise<string>;
          HelperStatus(): Promise<HelperStatus>;
          InstallHelper(): Promise<void>;
          UninstallHelper(): Promise<void>;
          GetMCPStatus(): Promise<MCPStatus>;
          SetMCPEnabled(enabled: boolean): Promise<void>;
          SetMCPPort(port: number): Promise<void>;
          SetMCPTokenEnabled(enabled: boolean): Promise<void>;
          RegenerateMCPToken(): Promise<string>;
          InstallMCPClient(client: string): Promise<MCPInstallResult>;
        };
      };
    };
    runtime?: {
      EventsOn(event: string, callback: (state: never) => void): () => void;
    };
  }
}

function api() {
  const app = window.go?.app?.App;
  if (!app) {
    throw new Error("Wails backend is unavailable. Run this interface with `wails dev`.");
  }
  return app;
}

export const backend = {
  bootstrap: () => Promise.resolve().then(() => api().Bootstrap()),
  reloadContexts: () => Promise.resolve().then(() => api().ReloadContexts()),
  addKubeconfig: () => Promise.resolve().then(() => api().AddKubeconfig()),
  removeKubeconfig: (path: string) =>
    Promise.resolve().then(() => api().RemoveKubeconfig(path)),
  probeContext: (contextName: string) =>
    Promise.resolve().then(() => api().ProbeContext(contextName)),
  rememberSelection: (contextName: string, namespace: string) =>
    Promise.resolve().then(() => api().RememberSelection(contextName, namespace)),
  namespaces: (contextName: string) =>
    Promise.resolve().then(() => api().Namespaces(contextName)),
  listServices: (contextName: string, namespace: string) =>
    Promise.resolve().then(() => api().ListServices(contextName, namespace)),
  listPods: (contextName: string, namespace: string) =>
    Promise.resolve().then(() => api().ListPods(contextName, namespace)),
  connect: (contextName: string, namespace: string) =>
    Promise.resolve().then(() => api().Connect(contextName, namespace)),
  connectMode: (contextName: string, namespace: string, mode: ConnectionMode) =>
    Promise.resolve().then(() => api().ConnectMode(contextName, namespace, mode)),
  disconnect: () => Promise.resolve().then(() => api().Disconnect()),
  testPortForward: (id: string) =>
    Promise.resolve().then(() => api().TestPortForward(id)),
  getManualNetwork: (contextName: string) =>
    Promise.resolve().then(() => api().GetManualNetwork(contextName)),
  setManualNetwork: (contextName: string, network: ManualNetwork) =>
    Promise.resolve().then(() => api().SetManualNetwork(contextName, network)),
  setDNSNamespace: (contextName: string, namespace: string) =>
    Promise.resolve().then(() => api().SetDNSNamespace(contextName, namespace)),
  getHostAliases: (contextName: string) =>
    Promise.resolve().then(() => api().GetHostAliases(contextName)),
  setHostAliases: (contextName: string, items: HostAlias[]) =>
    Promise.resolve().then(() => api().SetHostAliases(contextName, items)),
  gatewayInstallManifest: () =>
    Promise.resolve().then(() => api().GatewayInstallManifest()),
  startIntercept: (mapping: InterceptMapping) =>
    Promise.resolve().then(() => api().StartIntercept(mapping)),
  startMirror: (mapping: InterceptMapping) =>
    Promise.resolve().then(() => api().StartMirror(mapping)),
  stopIntercept: (id: string) => Promise.resolve().then(() => api().StopIntercept(id)),
  testIntercept: (id: string) =>
    Promise.resolve().then(() => api().TestIntercept(id)),
  listIntercepts: () => Promise.resolve().then(() => api().ListIntercepts()),
  listMirrors: () => Promise.resolve().then(() => api().ListMirrors()),
  startPreview: (request: PreviewRequest) =>
    Promise.resolve().then(() => api().StartPreview(request)),
  stopPreview: (id: string) => Promise.resolve().then(() => api().StopPreview(id)),
  listPreviews: () => Promise.resolve().then(() => api().ListPreviews()),
  startPortForward: (request: PortForwardRequest) =>
    Promise.resolve().then(() => api().StartPortForward(request)),
  stopPortForward: (id: string) => Promise.resolve().then(() => api().StopPortForward(id)),
  listPortForwards: () => Promise.resolve().then(() => api().ListPortForwards()),
  resetSessions: () => Promise.resolve().then(() => api().ResetSessions()),
  sessionIntentCounts: () =>
    Promise.resolve().then(() => api().SessionIntentCounts()),
  checkForUpdates: () => Promise.resolve().then(() => api().CheckForUpdates()),
  openUpdatePage: () => Promise.resolve().then(() => api().OpenUpdatePage()),
  getSingBoxConfig: () => Promise.resolve().then(() => api().GetSingBoxConfig()),
  helperStatus: () => Promise.resolve().then(() => api().HelperStatus()),
  installHelper: () => Promise.resolve().then(() => api().InstallHelper()),
  uninstallHelper: () => Promise.resolve().then(() => api().UninstallHelper()),
  getMCPStatus: () => Promise.resolve().then(() => api().GetMCPStatus()),
  setMCPEnabled: (enabled: boolean) =>
    Promise.resolve().then(() => api().SetMCPEnabled(enabled)),
  setMCPPort: (port: number) => Promise.resolve().then(() => api().SetMCPPort(port)),
  setMCPTokenEnabled: (enabled: boolean) =>
    Promise.resolve().then(() => api().SetMCPTokenEnabled(enabled)),
  regenerateMCPToken: () => Promise.resolve().then(() => api().RegenerateMCPToken()),
  installMCPClient: (client: string) =>
    Promise.resolve().then(() => api().InstallMCPClient(client)),
  onSession: (callback: (state: SessionState) => void) => {
    if (!window.runtime) return () => undefined;
    return window.runtime.EventsOn("session:state", callback as (state: never) => void);
  },
  onSessionMetrics: (callback: (metrics: Metrics) => void) => {
    if (!window.runtime) return () => undefined;
    return window.runtime.EventsOn(
      "session:metrics",
      callback as (metrics: never) => void,
    );
  },
  onUpdate: (callback: (state: UpdateInfo) => void) => {
    if (!window.runtime) return () => undefined;
    return window.runtime.EventsOn("update:state", callback as (state: never) => void);
  },
};
