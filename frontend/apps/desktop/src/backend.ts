import type {
  BootstrapData,
  ClusterInventory,
  ConnectionMode,
  ConnectivityTestResult,
  FileEntry,
  FileManagerTarget,
  FileTransferRequest,
  FileTransferTask,
  GatewayTransport,
  HelperStatus,
  HostAlias,
  InterceptInfo,
  InterceptMapping,
  ManualNetwork,
  MCPInstallResult,
  MCPStatus,
  PodInfo,
  PodSSHEnableRequest,
  PodSSHInfo,
  PortForwardInfo,
  PortForwardRequest,
  PreviewInfo,
  PreviewRequest,
  ProbeResult,
  ServiceInfo,
	SaveServerProfileRequest,
	ServerDiscovery,
	ServerProfileResult,
	ServerProfileState,
	AuthSession,
	RemoteInventory,
	DataPlaneStatus,
	ServerPortForwardInfo,
	ServerPortForwardRequest,
		ServerExchangeInfo,
		ServerExchangeRequest,
			ServerMirrorInfo,
			ServerMirrorRequest,
			ServerPreviewInfo,
			ServerPreviewRequest,
		ServerPodSSHInfo,
	ServerPodSSHRequest,
	ServerLocalFileEntry,
	ServerExecEvent,
	ServerExecRequest,
	ServerExecTask,
	ServerFileTransferRequest,
	ServerFileTransferTask,
	ServerPodFileTarget,
	ServerPodFileList,
	ServerPodFileTask,
	ServerNetworkSettings,
  Metrics,
  SessionIntentCounts,
  SessionState,
  UpdateInfo,
	TrafficInspectionQuery,
	TrafficInspectionResult,
	TrafficInspectionSettings,
} from "./types";

declare global {
  interface Window {
    go?: {
      app?: {
        App?: {
          Bootstrap(): Promise<BootstrapData>;
		  ServerProfiles(): Promise<ServerProfileState>;
		  TestServerAddress(serviceAddress: string): Promise<ServerDiscovery>;
		  SaveServerProfile(request: SaveServerProfileRequest): Promise<ServerProfileResult>;
		  SelectServerProfile(id: string): Promise<ServerProfileState>;
		  DeleteServerProfile(id: string): Promise<ServerProfileState>;
		  LoginServerOIDC(profileId: string, providerId: string): Promise<AuthSession>;
		  CancelServerLogin(): Promise<void>;
		  ServerAuthStatus(profileId: string): Promise<AuthSession>;
		  RefreshServerLogin(profileId: string): Promise<AuthSession>;
		  LogoutServer(profileId: string): Promise<void>;
		  LoadServerInventory(profileId: string, namespace: string): Promise<RemoteInventory>;
		  ConnectServerDataPlane(profileId: string, mode: "socks" | "tun"): Promise<DataPlaneStatus>;
		  DisconnectServerDataPlane(profileId: string): Promise<DataPlaneStatus>;
		  StartServerTunnel(profileId: string): Promise<DataPlaneStatus>;
		  StopServerTunnel(profileId: string): Promise<DataPlaneStatus>;
		  GetServerSingBoxConfig(profileId: string): Promise<string>;
		  GetServerNetworkSettings(profileId: string): Promise<ServerNetworkSettings>;
		  SetServerSOCKSPort(profileId: string, port: number): Promise<ServerNetworkSettings>;
		  SetServerDNSNamespace(profileId: string, namespace: string): Promise<ServerNetworkSettings>;
		  SetServerHostAliases(profileId: string, aliases: HostAlias[]): Promise<ServerNetworkSettings>;
		  ServerDataPlaneMetrics(profileId: string): Promise<Metrics>;
		  ServerDataPlaneLogs(profileId: string): Promise<string[]>;
		  TrafficInspectionEvents(query: TrafficInspectionQuery): Promise<TrafficInspectionResult>;
		  GetTrafficInspectionSettings(): Promise<TrafficInspectionSettings>;
		  SetTrafficInspectionEnabled(enabled: boolean): Promise<TrafficInspectionSettings>;
		  ImportTrafficInspectionProtoDirectory(): Promise<TrafficInspectionSettings>;
		  StartServerPortForward(request: ServerPortForwardRequest): Promise<ServerPortForwardInfo>;
		  StopServerPortForward(profileId: string, taskId: string): Promise<void>;
		  ListServerPortForwards(profileId: string): Promise<ServerPortForwardInfo[]>;
			  StartServerExchange(request: ServerExchangeRequest): Promise<ServerExchangeInfo>;
			  StopServerExchange(profileId: string, taskId: string): Promise<void>;
			  ListServerExchanges(profileId: string): Promise<ServerExchangeInfo[]>;
			  StartServerMirror(request: ServerMirrorRequest): Promise<ServerMirrorInfo>;
			  StopServerMirror(profileId: string, taskId: string): Promise<void>;
			  ListServerMirrors(profileId: string): Promise<ServerMirrorInfo[]>;
			  StartServerPreview(request: ServerPreviewRequest): Promise<ServerPreviewInfo>;
			  StopServerPreview(profileId: string, taskId: string): Promise<void>;
			  ListServerPreviews(profileId: string): Promise<ServerPreviewInfo[]>;
			  StartServerPodSSH(request: ServerPodSSHRequest): Promise<ServerPodSSHInfo>;
		  StopServerPodSSH(profileId: string, endpointId: string): Promise<void>;
		  ListServerPodSSH(profileId: string): Promise<ServerPodSSHInfo[]>;
		  OpenServerPodSSH(profileId: string, endpointId: string): Promise<void>;
		  StartServerExec(request: ServerExecRequest): Promise<ServerExecTask>;
		  WriteServerExecInput(profileId: string, taskId: string, input: string): Promise<void>;
		  ResizeServerExec(profileId: string, taskId: string, width: number, height: number): Promise<void>;
		  StopServerExec(profileId: string, taskId: string): Promise<void>;
		  StartServerFileTransfer(request: ServerFileTransferRequest): Promise<ServerFileTransferTask>;
		  ListServerFileTransfers(profileId: string): Promise<ServerFileTransferTask[]>;
		  CancelServerFileTransfer(profileId: string, taskId: string): Promise<void>;
		  ResumeServerFileTransfer(profileId: string, taskId: string): Promise<ServerFileTransferTask>;
		  ClearServerFileTransferHistory(profileId: string): Promise<void>;
		  PickServerUploadPath(kind: "file" | "directory"): Promise<string>;
		  PickServerDownloadPath(kind: "file" | "directory", suggestedName: string): Promise<string>;
		  ServerLocalHomeDirectory(): Promise<string>;
		  ListServerLocalFiles(path: string): Promise<ServerLocalFileEntry[]>;
		  CreateServerLocalFile(parent: string, name: string, kind: "file" | "directory"): Promise<void>;
		  RenameServerLocalFile(path: string, name: string): Promise<void>;
		  DeleteServerLocalFile(path: string): Promise<void>;
		  ListServerPodFiles(target: ServerPodFileTarget): Promise<ServerPodFileList>;
		  CreateServerPodFile(request: ServerPodFileTarget & { kind: "file" | "directory" }): Promise<ServerPodFileTask>;
		  RenameServerPodFile(request: ServerPodFileTarget & { destination: string }): Promise<ServerPodFileTask>;
		  DeleteServerPodFile(request: ServerPodFileTarget & { recursive?: boolean }): Promise<ServerPodFileTask>;
          ReloadContexts(): Promise<ClusterInventory>;
          AddKubeconfig(): Promise<ClusterInventory>;
          AddKubeconfigContent(content: string): Promise<ClusterInventory>;
          RemoveKubeconfig(path: string): Promise<ClusterInventory>;
          ProbeContext(contextName: string): Promise<ProbeResult>;
          RememberSelection(contextName: string, namespace: string): Promise<void>;
          Namespaces(contextName: string): Promise<string[]>;
          ListServices(contextName: string, namespace: string): Promise<ServiceInfo[]>;
          ListPods(contextName: string, namespace: string): Promise<PodInfo[]>;
          EnablePodSSH(request: PodSSHEnableRequest): Promise<PodSSHInfo>;
          DisablePodSSH(id: string): Promise<void>;
          ListPodSSH(): Promise<PodSSHInfo[]>;
          OpenPodSSHTerminal(id: string, container: string): Promise<void>;
          PickLocalDirectory(): Promise<string>;
          LocalHomeDirectory(): Promise<string>;
          ListLocalDirectory(path: string): Promise<FileEntry[]>;
          ListPodDirectory(target: FileManagerTarget, path: string): Promise<FileEntry[]>;
          CreateLocalDirectory(parent: string, name: string): Promise<void>;
          CreateLocalFile(parent: string, name: string): Promise<void>;
          CreatePodDirectory(
            target: FileManagerTarget,
            parent: string,
            name: string,
          ): Promise<void>;
          CreatePodFile(
            target: FileManagerTarget,
            parent: string,
            name: string,
          ): Promise<void>;
          RenameLocalPath(path: string, newName: string): Promise<void>;
          RenamePodPath(
            target: FileManagerTarget,
            path: string,
            newName: string,
          ): Promise<void>;
          DeleteLocalPath(path: string): Promise<void>;
          DeletePodPath(target: FileManagerTarget, path: string): Promise<void>;
          StartFileTransfer(request: FileTransferRequest): Promise<FileTransferTask>;
          ListFileTransfers(): Promise<FileTransferTask[]>;
          PauseFileTransfer(id: string): Promise<void>;
          ResumeFileTransfer(id: string): Promise<void>;
          CancelFileTransfer(id: string): Promise<void>;
          ClearFileTransferHistory(): Promise<void>;
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
          SetShareGateway(shared: boolean): Promise<void>;
          SetGatewayNamespace(namespace: string): Promise<void>;
          GetGatewayTransport(contextName: string): Promise<GatewayTransport>;
          SetGatewayTransport(contextName: string, config: GatewayTransport): Promise<void>;
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
      EventsOn<T = unknown>(event: string, callback: (state: T) => void): () => void;
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

function normalizeRemoteInventory(inventory: RemoteInventory): RemoteInventory {
  return {
    ...inventory,
    namespaces: Array.isArray(inventory.namespaces) ? inventory.namespaces : [],
    capabilities: Array.isArray(inventory.capabilities) ? inventory.capabilities : [],
    pods: Array.isArray(inventory.pods) ? inventory.pods : [],
    services: Array.isArray(inventory.services) ? inventory.services : [],
    namespace: inventory.namespace?.trim() || undefined,
  };
}

export const backend = {
  bootstrap: () => Promise.resolve().then(() => api().Bootstrap()),
	serverProfiles: () => Promise.resolve().then(() => api().ServerProfiles()),
	testServerAddress: (serviceAddress: string) =>
		Promise.resolve().then(() => api().TestServerAddress(serviceAddress)),
	saveServerProfile: (request: SaveServerProfileRequest) =>
		Promise.resolve().then(() => api().SaveServerProfile(request)),
	selectServerProfile: (id: string) =>
		Promise.resolve().then(() => api().SelectServerProfile(id)),
	deleteServerProfile: (id: string) =>
		Promise.resolve().then(() => api().DeleteServerProfile(id)),
	loginServerOIDC: (profileId: string, providerId: string) =>
		Promise.resolve().then(() => api().LoginServerOIDC(profileId, providerId)),
	cancelServerLogin: () => Promise.resolve().then(() => api().CancelServerLogin()),
	serverAuthStatus: (profileId: string) =>
		Promise.resolve().then(() => api().ServerAuthStatus(profileId)),
	refreshServerLogin: (profileId: string) =>
		Promise.resolve().then(() => api().RefreshServerLogin(profileId)),
	logoutServer: (profileId: string) =>
		Promise.resolve().then(() => api().LogoutServer(profileId)),
		loadServerInventory: (profileId: string, namespace = "") =>
			Promise.resolve()
				.then(() => api().LoadServerInventory(profileId, namespace))
				.then(normalizeRemoteInventory),
		connectServerDataPlane: (profileId: string, mode: "socks" | "tun") =>
			Promise.resolve().then(() => api().ConnectServerDataPlane(profileId, mode)),
		disconnectServerDataPlane: (profileId: string) =>
			Promise.resolve().then(() => api().DisconnectServerDataPlane(profileId)),
		startServerTunnel: (profileId: string) =>
		Promise.resolve().then(() => api().StartServerTunnel(profileId)),
	stopServerTunnel: (profileId: string) =>
		Promise.resolve().then(() => api().StopServerTunnel(profileId)),
	getServerNetworkSettings: (profileId: string) =>
		Promise.resolve().then(() => api().GetServerNetworkSettings(profileId)),
	setServerSOCKSPort: (profileId: string, port: number) =>
		Promise.resolve().then(() => api().SetServerSOCKSPort(profileId, port)),
	setServerDNSNamespace: (profileId: string, namespace: string) =>
		Promise.resolve().then(() => api().SetServerDNSNamespace(profileId, namespace)),
	setServerHostAliases: (profileId: string, aliases: HostAlias[]) =>
		Promise.resolve().then(() => api().SetServerHostAliases(profileId, aliases)),
	serverDataPlaneMetrics: (profileId: string) =>
		Promise.resolve().then(() => api().ServerDataPlaneMetrics(profileId)),
	serverDataPlaneLogs: (profileId: string) =>
		Promise.resolve().then(() => api().ServerDataPlaneLogs(profileId)),
	trafficInspectionEvents: (query: TrafficInspectionQuery) =>
		Promise.resolve().then(() => api().TrafficInspectionEvents(query)),
	getTrafficInspectionSettings: () =>
		Promise.resolve().then(() => api().GetTrafficInspectionSettings()),
	setTrafficInspectionEnabled: (enabled: boolean) =>
		Promise.resolve().then(() => api().SetTrafficInspectionEnabled(enabled)),
	importTrafficInspectionProtoDirectory: () =>
		Promise.resolve().then(() => api().ImportTrafficInspectionProtoDirectory()),
	startServerPortForward: (request: ServerPortForwardRequest) =>
		Promise.resolve().then(() => api().StartServerPortForward(request)),
	stopServerPortForward: (profileId: string, taskId: string) =>
		Promise.resolve().then(() => api().StopServerPortForward(profileId, taskId)),
	listServerPortForwards: (profileId: string) =>
		Promise.resolve().then(() => api().ListServerPortForwards(profileId)),
	startServerExchange: (request: ServerExchangeRequest) =>
		Promise.resolve().then(() => api().StartServerExchange(request)),
	stopServerExchange: (profileId: string, taskId: string) =>
		Promise.resolve().then(() => api().StopServerExchange(profileId, taskId)),
		listServerExchanges: (profileId: string) =>
			Promise.resolve().then(() => api().ListServerExchanges(profileId)),
		startServerMirror: (request: ServerMirrorRequest) =>
			Promise.resolve().then(() => api().StartServerMirror(request)),
		stopServerMirror: (profileId: string, taskId: string) =>
			Promise.resolve().then(() => api().StopServerMirror(profileId, taskId)),
			listServerMirrors: (profileId: string) =>
				Promise.resolve().then(() => api().ListServerMirrors(profileId)),
			startServerPreview: (request: ServerPreviewRequest) =>
				Promise.resolve().then(() => api().StartServerPreview(request)),
			stopServerPreview: (profileId: string, taskId: string) =>
				Promise.resolve().then(() => api().StopServerPreview(profileId, taskId)),
			listServerPreviews: (profileId: string) =>
				Promise.resolve().then(() => api().ListServerPreviews(profileId)),
		startServerPodSSH: (request: ServerPodSSHRequest) =>
		Promise.resolve().then(() => api().StartServerPodSSH(request)),
	stopServerPodSSH: (profileId: string, endpointId: string) =>
		Promise.resolve().then(() => api().StopServerPodSSH(profileId, endpointId)),
	listServerPodSSH: (profileId: string) =>
		Promise.resolve().then(() => api().ListServerPodSSH(profileId)),
	openServerPodSSH: (profileId: string, endpointId: string) =>
		Promise.resolve().then(() => api().OpenServerPodSSH(profileId, endpointId)),
	startServerExec: (request: ServerExecRequest) =>
		Promise.resolve().then(() => api().StartServerExec(request)),
	writeServerExecInput: (profileId: string, taskId: string, input: string) =>
		Promise.resolve().then(() => api().WriteServerExecInput(profileId, taskId, input)),
	resizeServerExec: (profileId: string, taskId: string, width: number, height: number) =>
		Promise.resolve().then(() => api().ResizeServerExec(profileId, taskId, width, height)),
	stopServerExec: (profileId: string, taskId: string) =>
		Promise.resolve().then(() => api().StopServerExec(profileId, taskId)),
	startServerFileTransfer: (request: ServerFileTransferRequest) =>
		Promise.resolve().then(() => api().StartServerFileTransfer(request)),
	listServerFileTransfers: (profileId: string) =>
		Promise.resolve().then(() => api().ListServerFileTransfers(profileId)),
	cancelServerFileTransfer: (profileId: string, taskId: string) =>
		Promise.resolve().then(() => api().CancelServerFileTransfer(profileId, taskId)),
	resumeServerFileTransfer: (profileId: string, taskId: string) =>
		Promise.resolve().then(() => api().ResumeServerFileTransfer(profileId, taskId)),
	clearServerFileTransferHistory: (profileId: string) =>
		Promise.resolve().then(() => api().ClearServerFileTransferHistory(profileId)),
	pickServerUploadPath: (kind: "file" | "directory") =>
		Promise.resolve().then(() => api().PickServerUploadPath(kind)),
	pickServerDownloadPath: (kind: "file" | "directory", suggestedName: string) =>
		Promise.resolve().then(() => api().PickServerDownloadPath(kind, suggestedName)),
	serverLocalHomeDirectory: () => Promise.resolve().then(() => api().ServerLocalHomeDirectory()),
	listServerLocalFiles: (path: string) => Promise.resolve().then(() => api().ListServerLocalFiles(path)),
	createServerLocalFile: (parent: string, name: string, kind: "file" | "directory") =>
		Promise.resolve().then(() => api().CreateServerLocalFile(parent, name, kind)),
	renameServerLocalFile: (path: string, name: string) =>
		Promise.resolve().then(() => api().RenameServerLocalFile(path, name)),
	deleteServerLocalFile: (path: string) => Promise.resolve().then(() => api().DeleteServerLocalFile(path)),
	listServerPodFiles: (target: ServerPodFileTarget) =>
		Promise.resolve().then(() => api().ListServerPodFiles(target)),
	createServerPodFile: (request: ServerPodFileTarget & { kind: "file" | "directory" }) =>
		Promise.resolve().then(() => api().CreateServerPodFile(request)),
	renameServerPodFile: (request: ServerPodFileTarget & { destination: string }) =>
		Promise.resolve().then(() => api().RenameServerPodFile(request)),
	deleteServerPodFile: (request: ServerPodFileTarget & { recursive?: boolean }) =>
		Promise.resolve().then(() => api().DeleteServerPodFile(request)),
  reloadContexts: () => Promise.resolve().then(() => api().ReloadContexts()),
  addKubeconfig: () => Promise.resolve().then(() => api().AddKubeconfig()),
  addKubeconfigContent: (content: string) =>
    Promise.resolve().then(() => api().AddKubeconfigContent(content)),
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
  enablePodSSH: (request: PodSSHEnableRequest) =>
    Promise.resolve().then(() => api().EnablePodSSH(request)),
  disablePodSSH: (id: string) =>
    Promise.resolve().then(() => api().DisablePodSSH(id)),
  listPodSSH: () => Promise.resolve().then(() => api().ListPodSSH()),
  openPodSSHTerminal: (id: string, container: string) =>
    Promise.resolve().then(() => api().OpenPodSSHTerminal(id, container)),
  pickLocalDirectory: () => Promise.resolve().then(() => api().PickLocalDirectory()),
  localHomeDirectory: () => Promise.resolve().then(() => api().LocalHomeDirectory()),
  listLocalDirectory: (path: string) =>
    Promise.resolve().then(() => api().ListLocalDirectory(path)),
  listPodDirectory: (target: FileManagerTarget, path: string) =>
    Promise.resolve().then(() => api().ListPodDirectory(target, path)),
  createLocalDirectory: (parent: string, name: string) =>
    Promise.resolve().then(() => api().CreateLocalDirectory(parent, name)),
  createLocalFile: (parent: string, name: string) =>
    Promise.resolve().then(() => api().CreateLocalFile(parent, name)),
  createPodDirectory: (target: FileManagerTarget, parent: string, name: string) =>
    Promise.resolve().then(() => api().CreatePodDirectory(target, parent, name)),
  createPodFile: (target: FileManagerTarget, parent: string, name: string) =>
    Promise.resolve().then(() => api().CreatePodFile(target, parent, name)),
  renameLocalPath: (path: string, newName: string) =>
    Promise.resolve().then(() => api().RenameLocalPath(path, newName)),
  renamePodPath: (target: FileManagerTarget, path: string, newName: string) =>
    Promise.resolve().then(() => api().RenamePodPath(target, path, newName)),
  deleteLocalPath: (path: string) =>
    Promise.resolve().then(() => api().DeleteLocalPath(path)),
  deletePodPath: (target: FileManagerTarget, path: string) =>
    Promise.resolve().then(() => api().DeletePodPath(target, path)),
  startFileTransfer: (request: FileTransferRequest) =>
    Promise.resolve().then(() => api().StartFileTransfer(request)),
  listFileTransfers: () => Promise.resolve().then(() => api().ListFileTransfers()),
  pauseFileTransfer: (id: string) =>
    Promise.resolve().then(() => api().PauseFileTransfer(id)),
  resumeFileTransfer: (id: string) =>
    Promise.resolve().then(() => api().ResumeFileTransfer(id)),
  cancelFileTransfer: (id: string) =>
    Promise.resolve().then(() => api().CancelFileTransfer(id)),
  clearFileTransferHistory: () =>
    Promise.resolve().then(() => api().ClearFileTransferHistory()),
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
  setShareGateway: (shared: boolean) =>
    Promise.resolve().then(() => api().SetShareGateway(shared)),
  setGatewayNamespace: (namespace: string) =>
    Promise.resolve().then(() => api().SetGatewayNamespace(namespace)),
  getGatewayTransport: (contextName: string) =>
    Promise.resolve().then(() => api().GetGatewayTransport(contextName)),
  setGatewayTransport: (contextName: string, config: GatewayTransport) =>
    Promise.resolve().then(() => api().SetGatewayTransport(contextName, config)),
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
	getServerSingBoxConfig: (profileId: string) =>
		Promise.resolve().then(() => api().GetServerSingBoxConfig(profileId)),
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
  onTransfer: (callback: (task: FileTransferTask) => void) => {
    if (!window.runtime) return () => undefined;
    return window.runtime.EventsOn(
      "transfer:updated",
      callback as (state: never) => void,
    );
  },
	onServerExec: (callback: (event: ServerExecEvent) => void) => {
		if (!window.runtime) return () => undefined;
		return window.runtime.EventsOn(
			"server-exec:event",
			callback as (event: never) => void,
		);
	},
	onServerFileTransfer: (callback: (task: ServerFileTransferTask) => void) => {
		if (!window.runtime) return () => undefined;
		return window.runtime.EventsOn(
			"server-file-transfer:event",
			callback as (task: never) => void,
		);
	},
};
