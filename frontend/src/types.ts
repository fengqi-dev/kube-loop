interface NetworkDiagnostic {
  code: string;
  severity: "info" | "warning";
  message: string;
  target?: string;
  conflict?: string;
  interface?: string;
}

interface NetworkDiagnostics {
  routingMode: "native" | "vnat" | "proxy";
  strictRoute: boolean;
  issues?: NetworkDiagnostic[];
}

interface UpdateInfo {
  currentVersion: string;
  latestVersion?: string;
  available: boolean;
  url: string;
  publishedAt?: string;
  checkedAt?: string;
  error?: string;
}

export interface ServerProfile {
  id: string;
  baseUrl: string;
  tunnelPath: string;
  displayName?: string;
  lastPrincipalId?: string;
  lastUserName?: string;
  lastNamespace?: string;
}

export interface ServerProfileState {
  version: number;
  activeProfileId?: string;
  profiles: ServerProfile[];
}

interface AuthMethod {
  id: string;
  type: "oidc" | "ad" | "static-token" | "anonymous";
  displayName?: string;
  interaction: "browser" | "password" | "token" | "none";
}

export interface ServerDiscovery {
  serviceId: string;
  publicUrl: string;
  tunnelPath: string;
  apiVersions: string[];
  authMethods: AuthMethod[];
  features: string[];
  serverVersion: string;
  serverCommit?: string;
  protocolMin: string;
  protocolMax: string;
  minClientVersion?: string;
}

export interface SaveServerProfileRequest {
  baseUrl: string;
  displayName?: string;
  activate: boolean;
}

export interface ServerProfileResult {
  profile: ServerProfile;
  discovery: ServerDiscovery;
}

export interface AuthSession {
  authenticated: boolean;
  accessExpiresAt?: string;
  refreshExpiresAt?: string;
}

interface RemoteNamespace {
  name: string;
  status?: string;
}

export interface RemotePod {
  name: string;
  namespace: string;
  phase?: string;
  podIp?: string;
  nodeName?: string;
  ready: boolean;
  containers: string[];
}

interface RemoteServicePort {
  name?: string;
  port: number;
  protocol: string;
  targetPort?: string;
}

interface RemoteService {
  name: string;
  namespace: string;
  type: string;
  clusterIp?: string;
  externalName?: string;
  ports: RemoteServicePort[];
}

interface RemoteSession {
  id: string;
  namespace: string;
  state: string;
  generation: number;
  createdAt: string;
  updatedAt: string;
  lastHeartbeatAt: string;
  expiresAt: string;
  networkSpec: {
    version: number;
    podCIDRs: string[];
    serviceCIDRs: string[];
    serviceIPs: string[];
    dnsServer?: string;
    clusterDomains: string[];
  };
  networkSpecHash: string;
}

export interface DataPlaneStatus {
  state: string;
  mode: "socks" | "tun";
  sessionId: string;
  sessionGeneration: number;
  socksAddress: string;
  networkSpecHash: string;
}

export interface DataPlaneStatusEvent {
  profileId: string;
  status: DataPlaneStatus;
  error?: string;
  reason?:
    | "transport_interrupted"
    | "authentication_required"
    | "access_denied"
    | "session_expired"
    | "session_changed"
    | "network_unavailable";
  retryable?: boolean;
}

export interface ServerPortForwardRequest {
  profileId: string;
  kind: "pod" | "service";
  name: string;
  protocol?: "tcp" | "udp";
  remotePort: number;
  localPort: number;
}

export interface ServerPortForwardInfo {
  id: string;
  profileId: string;
  sessionId: string;
  namespace: string;
  kind: string;
  name: string;
  protocol: string;
  remotePort: number;
  localPort: number;
  address: string;
  dialAddress: string;
  state: string;
}

interface ServerExchangeTarget {
  servicePort: number;
  protocol: "tcp" | "udp";
  localHost: string;
  localPort: number;
}

export interface ServerExchangeRequest {
  profileId: string;
  service: string;
  targets: ServerExchangeTarget[];
}

export interface ServerExchangeInfo {
  id: string;
  profileId: string;
  sessionId: string;
  namespace: string;
  service: string;
  clusterIp: string;
  state: string;
  targets: ServerExchangeTarget[];
}

interface ServerMirrorTarget {
  servicePort: number;
  protocol: "tcp" | "udp";
  localHost: string;
  localPort: number;
}

export interface ServerMirrorRequest {
  profileId: string;
  service: string;
  targets: ServerMirrorTarget[];
}

export interface ServerMirrorInfo {
  id: string;
  profileId: string;
  sessionId: string;
  namespace: string;
  service: string;
  clusterIp: string;
  state: string;
  targets: ServerMirrorTarget[];
}

interface ServerPreviewTarget {
  servicePort: number;
  protocol: "tcp" | "udp";
  localHost: string;
  localPort: number;
}

export interface ServerPreviewRequest {
  profileId: string;
  name: string;
  targets: ServerPreviewTarget[];
}

export interface ServerPreviewInfo {
  id: string;
  profileId: string;
  sessionId: string;
  namespace: string;
  name: string;
  clusterIp: string;
  state: string;
  targets: ServerPreviewTarget[];
}

export interface ServerPodSSHRequest {
  profileId: string;
  pod: string;
  container?: string;
}

export interface ServerPodSSHInfo {
  id: string;
  profileId: string;
  sessionId: string;
  namespace: string;
  pod: string;
  container: string;
  containers: string[];
  podIp: string;
  address: string;
  port: number;
  command: string;
  state: string;
}

export interface ServerExecRequest {
  profileId: string;
  pod: string;
  container?: string;
  command: string[];
  tty: boolean;
  width?: number;
  height?: number;
}

export interface ServerExecTask {
  id: string;
  sessionId: string;
  namespace: string;
  state: "pending" | "running" | "stopped" | "failed";
  pod: string;
  container?: string;
  tty: boolean;
  createdAt: string;
  updatedAt: string;
  expiresAt: string;
}

export interface ServerExecEvent {
  profileId: string;
  taskId: string;
  type: "stdout" | "stderr" | "exit" | "error";
  data?: string;
  exitCode?: number;
  cancelled?: boolean;
  error?: string;
}

export interface ServerFileTransferRequest {
  profileId: string;
  direction: "upload" | "download";
  kind: "file" | "directory";
  pod: string;
  container?: string;
  localPath: string;
  remotePath: string;
  overwrite?: boolean;
}

export interface ServerFileTransferTask extends ServerFileTransferRequest {
  id: string;
  sessionId: string;
  namespace: string;
  status:
    | "queued"
    | "preparing"
    | "running"
    | "completed"
    | "failed"
    | "cancelled"
    | "interrupted";
  totalBytes?: number;
  doneBytes?: number;
  checksum?: string;
  resumeId?: string;
  temporaryPath?: string;
  error?: string;
  createdAt: string;
  updatedAt: string;
  completedAt?: string;
}

export interface ServerPodFileTarget {
  profileId: string;
  pod: string;
  container?: string;
  path: string;
}

export interface ServerPodFileEntry {
  name: string;
  path: string;
  kind: "file" | "directory" | "symlink" | "other";
  size: number;
  mode: string;
  modifiedAt: string;
}

export interface ServerPodFileList {
  sessionId: string;
  namespace: string;
  pod: string;
  container: string;
  path: string;
  items: ServerPodFileEntry[];
}

export interface ServerPodFileTask {
  id: string;
  sessionId: string;
  namespace: string;
  state: "pending" | "running" | "stopped" | "failed";
  action: "create" | "rename" | "delete";
  pod: string;
  container: string;
  path: string;
  destination?: string;
  kind?: "file" | "directory";
  recursive?: boolean;
  result: { completed: boolean; error?: string };
  createdAt: string;
  updatedAt: string;
  expiresAt: string;
}

export interface RemoteInventory {
  kubernetesVersion: string;
  gatewayVersion: string;
  namespaces: RemoteNamespace[];
  namespace?: string;
  capabilities: string[];
  pods: RemotePod[];
  services: RemoteService[];
  session?: RemoteSession;
  network?: NetworkDiagnostics;
  dataPlane?: DataPlaneStatus;
}

interface RemoteInventorySnapshot {
  schemaVersion: 1;
  type: "snapshot";
  resource: "pods" | "services";
  namespace: string;
  resourceVersion?: string;
  sequence: number;
  generatedAt: string;
  pods?: RemotePod[];
  services?: RemoteService[];
}

export interface ServerInventoryEvent {
  profileId: string;
  namespace: string;
  resource: "pods" | "services";
  snapshot?: RemoteInventorySnapshot;
  error?: string;
}

export interface BootstrapData {
  update: UpdateInfo;
  platform: string;
  mode: "setup" | "v2";
  serverProfiles: ServerProfileState;
  migration: V1MigrationStatus;
}

export interface V1MigrationStatus {
  legacyDetected: boolean;
  backupPath?: string;
  completedAt?: string;
  error?: string;
}
