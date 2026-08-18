export type Phase =
  | "idle"
  | "checking"
  | "installing-gateway"
  | "discovering-network"
  | "starting-tunnel"
  | "connected"
  | "error";

export type ConnectionMode = "tun" | "socks";

export interface GatewayTransport {
  mode: "port-forward" | "websocket";
  url?: string;
  token?: string;
  insecureSkipVerify?: boolean;
  poolSize?: number;
  maxPhysical?: number;
  maxStreams?: number;
}

export interface ContextInfo {
  name: string;
  cluster: string;
  server?: string;
  user?: string;
  namespace?: string;
  source?: string;
  current: boolean;
}

export interface KubeconfigFileInfo {
  path: string;
  default: boolean;
}

export interface ClusterInventory {
  contexts: ContextInfo[];
  files: KubeconfigFileInfo[];
}

export interface ProbeResult {
  context: string;
  ok: boolean;
  version?: string;
  latencyMs?: number;
  error?: string;
}

export interface Discovery {
  podCIDRs: string[];
  serviceCIDRs: string[];
  serviceIPs: string[];
  dnsServer: string;
  clusterDomains?: string[];
  pods: number;
  services: number;
  deployments: number;
}

export interface Capabilities {
  gatewayInstall: boolean;
  gatewayPortForward: boolean;
  clusterNodes: boolean;
  inventoryCluster: boolean;
  serviceWrite: boolean;
  serviceCreate: boolean;
  podExec: boolean;
  scopeNamespaces?: string[];
  issues?: string[];
}

export interface NetworkDiagnostic {
  code: string;
  severity: "info" | "warning";
  message: string;
  target?: string;
  conflict?: string;
  interface?: string;
}

export interface NetworkDiagnostics {
  routingMode: "native" | "vnat" | "proxy";
  strictRoute: boolean;
  issues?: NetworkDiagnostic[];
}

export interface ManualNetwork {
  podCIDRs?: string[];
  serviceCIDRs?: string[];
  dnsServer?: string;
  clusterDomains?: string[];
  dnsNamespace?: string;
}

export interface HostAlias {
  domain: string;
  ip: string;
}

export interface ServerNetworkSettings {
  dnsNamespace?: string;
  socksPort: number;
  hostAliases?: HostAlias[];
}

export interface Connection {
  id: string;
  network: string;
  source: string;
  destination: string;
  process: string;
  upload: number;
  download: number;
  uploadSpeed?: number;
  downloadSpeed?: number;
  startedAt: string;
  inbound: string;
  feature?: string;
  outbound: string;
  rule: string;
}

export interface Metrics {
  downloadTotal: number;
  uploadTotal: number;
  memory?: number;
  activeConnections?: number;
  connections: Connection[];
}

export interface UpdateInfo {
  currentVersion: string;
  latestVersion?: string;
  available: boolean;
  url: string;
  publishedAt?: string;
  checkedAt?: string;
  error?: string;
}

export interface LogEvent {
  time: string;
  level: string;
  message: string;
}

export interface SessionState {
  phase: Phase;
  mode?: ConnectionMode;
  context: string;
  namespace: string;
  dnsNamespace?: string;
  message: string;
  error?: string;
  dnsWarning?: string;
  network?: NetworkDiagnostics;
  discovery?: Discovery;
  capabilities?: Capabilities;
  scopeNamespaces?: string[];
  gatewayManifest?: string;
  pods?: PodInfo[];
  services?: ServiceInfo[];
  events?: LogEvent[];
  coreVersion?: string;
  socksPort?: number;
  kubernetesVersion?: string;
  connectedAt?: string;
  metrics?: Metrics;
  /** Bumps on Informer inventory changes only; not on metrics ticks. */
  inventoryRevision?: number;
  updatedAt: string;
}

export interface ConnectivityTestResult {
  passed: boolean;
  failedLayer?: "gateway-control" | "local-listener" | "local-target";
  error?: string;
}

export interface ServerProfile {
  id: string;
  baseUrl: string;
  tunnelPath: string;
  displayName?: string;
  lastIdentityId?: string;
  lastUserName?: string;
  lastNamespace?: string;
  socksPort?: number;
}

export interface ServerProfileState {
  version: number;
  activeProfileId?: string;
  profiles: ServerProfile[];
}

export interface AuthMethod {
  id: string;
  type: "oidc" | "local";
  displayName?: string;
  interaction: "browser" | "none";
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
  id?: string;
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
  userName?: string;
  accessExpiresAt?: string;
  refreshExpiresAt?: string;
}

export interface RemoteNamespace {
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
  ports: RemotePodPort[];
}

export interface RemotePodPort {
  name?: string;
  port: number;
  protocol: string;
}

export interface RemoteServicePort {
  name?: string;
  port: number;
  protocol: string;
  targetPort?: string;
}

export interface RemoteService {
  name: string;
  namespace: string;
  type: string;
  clusterIp?: string;
  externalName?: string;
  ports: RemoteServicePort[];
}

export interface RemoteSession {
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
  reason?: "transport_interrupted" | "network_spec_changed" | "authentication_required" | "access_denied" | "session_expired" | "session_changed" | "network_unavailable" | "system_resumed";
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

export interface ServerExchangeTarget {
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

export interface ServerMirrorTarget {
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

export interface ServerPreviewTarget {
  servicePort: number;
  protocol: "tcp" | "udp";
  localHost: string;
  localPort: number;
}

export interface ServerPreviewRequest {
  profileId: string;
  namespace: string;
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
  status: "queued" | "preparing" | "running" | "completed" | "failed" | "cancelled" | "interrupted";
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

export interface ServerLocalFileEntry {
  name: string;
  path: string;
  kind: "file" | "directory" | "symlink" | "other";
  size: number;
  mode: number;
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

export interface RemoteInventorySnapshot {
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
  coreVersion: string;
  serverProfiles: ServerProfileState;
}

export type TrafficInspectionProtocol = "http" | "https" | "grpc" | "grpcs";

export interface TrafficInspectionRaw {
  format: "http" | "grpc";
  direction: string;
  encoding: string;
  data: string;
  size: number;
  truncated: boolean;
}

export interface TrafficInspectionHTTP {
  version: string;
  method?: string;
  host?: string;
  path?: string;
  status?: number;
  request_headers?: Record<string, string[]>;
  response_headers?: Record<string, string[]>;
}

export interface TrafficInspectionGRPC {
  service?: string;
  method?: string;
  path?: string;
  status?: string;
}

export interface TrafficInspectionEvent {
  schema_version: number;
  event_id: string;
  flow_id: string;
  timestamp: string;
  type: "request" | "response" | "body";
  protocol: TrafficInspectionProtocol;
  tls: boolean;
  destination: string;
  duration_ms?: number;
  http?: TrafficInspectionHTTP;
  grpc?: TrafficInspectionGRPC;
  raw?: TrafficInspectionRaw;
}

export interface TrafficInspectionQuery {
  host: string;
  path: string;
  limit: number;
}

export interface TrafficInspectionResult {
  enabled: boolean;
  events: TrafficInspectionEvent[];
}

export interface TrafficInspectionSettings {
  enabled: boolean;
}

export interface HelperStatus {
  installed: boolean;
  running: boolean;
  version?: string;
  expected: string;
  socket: string;
  error?: string;
}

export interface ServicePortInfo {
  name: string;
  port: number;
  protocol: string;
}

export interface ServiceInfo {
  name: string;
  namespace: string;
  type: string;
  clusterIP: string;
  ports: ServicePortInfo[];
}

export interface InterceptPortMapping {
  servicePort: number;
  protocol: string;
  localHost: string;
  localPort: number;
}

export interface InterceptMapping {
  namespace: string;
  service: string;
  ports: InterceptPortMapping[];
}

export interface InterceptPort {
  name: string;
  protocol: string;
  servicePort: number;
  listenPort: number;
}

export interface InterceptInfo {
  id: string;
  namespace: string;
  service: string;
  mode?: string;
  ports: InterceptPort[];
  locals: InterceptPortMapping[];
}

export interface PreviewRequest {
  namespace: string;
  name: string;
  ports: InterceptPortMapping[];
}

export interface PreviewInfo {
  id: string;
  namespace: string;
  service: string;
  clusterIP?: string;
  preview?: boolean;
  ports: InterceptPort[];
  locals: InterceptPortMapping[];
}

export interface PodPortInfo {
  name: string;
  port: number;
  protocol: string;
}

export interface PodInfo {
  name: string;
  uid?: string;
  namespace: string;
  phase: string;
  ready: boolean;
  ip?: string;
  node?: string;
  containers: string[];
  ports: PodPortInfo[];
}

export interface PodSSHEnableRequest {
  context: string;
  namespace: string;
  pod: string;
  container?: string;
}

export interface PodSSHInfo {
  id: string;
  context: string;
  namespace: string;
  pod: string;
  container: string;
  ip: string;
  port: number;
  command: string;
}

export interface FileManagerTarget {
  context: string;
  namespace: string;
  pod: string;
  podUID?: string;
  container: string;
}

export interface FileEntry {
  name: string;
  path: string;
  dir: boolean;
  size: number;
  mode: number;
  modTime: string;
}

export type FileTransferDirection = "upload" | "download";
export type FileTransferStatus =
  | "queued"
  | "running"
  | "paused"
  | "completed"
  | "failed"
  | "cancelled"
  | "stale";

export interface FileTransferRequest {
  direction: FileTransferDirection;
  target: FileManagerTarget;
  sourcePath: string;
  destinationDir: string;
  overwrite: boolean;
}

export interface FileTransferTask {
  id: string;
  direction: FileTransferDirection;
  target: FileManagerTarget;
  sourcePath: string;
  destinationPath: string;
  tempPath?: string;
  directory?: boolean;
  status: FileTransferStatus;
  totalBytes: number;
  doneBytes: number;
  sourceModTime: string;
  overwrite: boolean;
  error?: string;
  createdAt: string;
  updatedAt: string;
  completedAt?: string;
}

export interface PortForwardRequest {
  context: string;
  namespace: string;
  kind: "pod" | "service" | string;
  name: string;
  protocol?: string;
  remotePort: number;
  localPort: number;
}

export interface PortForwardInfo {
  id: string;
  context: string;
  namespace: string;
  kind: string;
  name: string;
  podName: string;
  protocol: string;
  remotePort: number;
  localPort: number;
  address: string;
}

export interface SessionIntentCounts {
  podPortForwards: number;
  networkPortForwards: number;
  exchanges: number;
  mirrors: number;
}

export interface MCPStatus {
  enabled: boolean;
  listening: boolean;
  url?: string;
  port: number;
  tokenEnabled: boolean;
  token?: string;
  error?: string;
}

export type MCPClient = "claude" | "codex" | "cursor" | "vscode";

export interface MCPInstallResult {
  client: string;
  path: string;
}
