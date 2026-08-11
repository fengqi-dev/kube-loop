import type {
  AuthSession,
  BootstrapData,
  DataPlaneStatus,
  RemoteInventory,
  SaveServerProfileRequest,
  ServerDiscovery,
  ServerExchangeInfo,
  ServerExchangeRequest,
  ServerExecEvent,
  ServerExecRequest,
  ServerExecTask,
  ServerFileTransferRequest,
  ServerFileTransferTask,
  ServerMirrorInfo,
  ServerMirrorRequest,
  ServerPodFileList,
  ServerPodFileTarget,
  ServerPodFileTask,
  ServerPodSSHInfo,
  ServerPodSSHRequest,
  ServerPortForwardInfo,
  ServerPortForwardRequest,
  ServerPreviewInfo,
  ServerPreviewRequest,
  ServerProfileResult,
  ServerProfileState,
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
          LoginServerAD(
            profileId: string,
            providerId: string,
            username: string,
            password: string,
          ): Promise<AuthSession>;
          LoginServerStaticToken(
            profileId: string,
            providerId: string,
            staticToken: string,
          ): Promise<AuthSession>;
          LoginServerAnonymous(profileId: string, providerId: string): Promise<AuthSession>;
          ServerAuthStatus(profileId: string): Promise<AuthSession>;
          RefreshServerLogin(profileId: string): Promise<AuthSession>;
          LogoutServer(profileId: string): Promise<void>;
          LoadServerInventory(profileId: string, namespace: string): Promise<RemoteInventory>;
          StartServerTunnel(profileId: string): Promise<DataPlaneStatus>;
          StopServerTunnel(profileId: string): Promise<DataPlaneStatus>;
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
          ResizeServerExec(
            profileId: string,
            taskId: string,
            width: number,
            height: number,
          ): Promise<void>;
          StopServerExec(profileId: string, taskId: string): Promise<void>;
          StartServerFileTransfer(
            request: ServerFileTransferRequest,
          ): Promise<ServerFileTransferTask>;
          ListServerFileTransfers(profileId: string): Promise<ServerFileTransferTask[]>;
          CancelServerFileTransfer(profileId: string, taskId: string): Promise<void>;
          ResumeServerFileTransfer(
            profileId: string,
            taskId: string,
          ): Promise<ServerFileTransferTask>;
          ClearServerFileTransferHistory(profileId: string): Promise<void>;
          PickServerUploadPath(kind: "file" | "directory"): Promise<string>;
          PickServerDownloadPath(
            kind: "file" | "directory",
            suggestedName: string,
          ): Promise<string>;
          ListServerPodFiles(target: ServerPodFileTarget): Promise<ServerPodFileList>;
          CreateServerPodFile(
            request: ServerPodFileTarget & { kind: "file" | "directory" },
          ): Promise<ServerPodFileTask>;
          RenameServerPodFile(
            request: ServerPodFileTarget & { destination: string },
          ): Promise<ServerPodFileTask>;
          DeleteServerPodFile(
            request: ServerPodFileTarget & { recursive?: boolean },
          ): Promise<ServerPodFileTask>;
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
  loginServerAD: (profileId: string, providerId: string, username: string, password: string) =>
    Promise.resolve().then(() => api().LoginServerAD(profileId, providerId, username, password)),
  loginServerStaticToken: (profileId: string, providerId: string, staticToken: string) =>
    Promise.resolve().then(() => api().LoginServerStaticToken(profileId, providerId, staticToken)),
  loginServerAnonymous: (profileId: string, providerId: string) =>
    Promise.resolve().then(() => api().LoginServerAnonymous(profileId, providerId)),
  serverAuthStatus: (profileId: string) =>
    Promise.resolve().then(() => api().ServerAuthStatus(profileId)),
  refreshServerLogin: (profileId: string) =>
    Promise.resolve().then(() => api().RefreshServerLogin(profileId)),
  logoutServer: (profileId: string) => Promise.resolve().then(() => api().LogoutServer(profileId)),
  loadServerInventory: (profileId: string, namespace = "") =>
    Promise.resolve().then(() => api().LoadServerInventory(profileId, namespace)),
  startServerTunnel: (profileId: string) =>
    Promise.resolve().then(() => api().StartServerTunnel(profileId)),
  stopServerTunnel: (profileId: string) =>
    Promise.resolve().then(() => api().StopServerTunnel(profileId)),
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
  listServerPodFiles: (target: ServerPodFileTarget) =>
    Promise.resolve().then(() => api().ListServerPodFiles(target)),
  createServerPodFile: (request: ServerPodFileTarget & { kind: "file" | "directory" }) =>
    Promise.resolve().then(() => api().CreateServerPodFile(request)),
  renameServerPodFile: (request: ServerPodFileTarget & { destination: string }) =>
    Promise.resolve().then(() => api().RenameServerPodFile(request)),
  deleteServerPodFile: (request: ServerPodFileTarget & { recursive?: boolean }) =>
    Promise.resolve().then(() => api().DeleteServerPodFile(request)),
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
