export type Phase =
  | "idle"
  | "checking"
  | "installing-gateway"
  | "discovering-network"
  | "starting-tunnel"
  | "connected"
  | "error";

export type ConnectionMode = "tun" | "socks";

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

export interface BootstrapData {
  contexts: ContextInfo[];
  namespaces: string[];
  session: SessionState;
  update: UpdateInfo;
  preferredContext?: string;
  preferredNamespace?: string;
  preferredMode?: ConnectionMode;
  platform?: string;
  kubeconfigFiles?: KubeconfigFileInfo[];
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
