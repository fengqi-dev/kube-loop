export type ViewKey =
  | "overview"
  | "policy"
  | "delegations"
  | "providers"
  | "oauthClients"
  | "users"
  | "security"
  | "principals"
  | "sessions"
  | "tasks"
  | "relays"
  | "audit";
export interface Scope {
  namespace: string;
  capabilities?: string[];
}
export interface Capabilities {
  capabilities: string[];
  namespaceScopes: Scope[];
  authenticationType: string;
  policyRevision: number;
}
export interface Bootstrap {
  identity: { id: string; groups: string[] };
  session: {
    authenticationType: string;
    createdAt: string;
    lastSeenAt: string;
    idleExpiresAt: string;
    absoluteExpiresAt: string;
  };
  authorization: {
    capabilities: string[];
    namespaceScopes: Scope[];
    policyRevision: number;
    policyEtag: number;
  };
}
export interface Overview {
  generatedAt: string;
  system: {
    controlPlane: {
      version: string;
      commit: string;
      protocolMin: string;
      protocolMax: string;
    };
    storage: { status: string; backend: string; schemaVersion: number };
  };
  security: {
    authenticationType: string;
    policyRevision: number;
    policyEtag: number;
  };
  runtime: {
    activeSessions: { count: number; truncated: boolean };
    activeTasks: { count: number; truncated: boolean };
    relays: {
      total: number;
      online: number;
      ready: number;
      draining: number;
      reservations: number;
    };
  };
  recentAudit: Record<string, unknown>[];
}
export interface Assignment {
  id: string;
  subject: {
    type: "principal" | "group";
    principalId?: string;
    providerId?: string;
    groupName?: string;
  };
  roleId: string;
  scope: {
    type: "platform" | "namespaces";
    names?: string[];
    labelSelectors?: Array<{
      matchLabels?: Record<string, string>;
      matchExpressions?: Array<{ key: string; operator: string; values?: string[] }>;
    }>;
  };
  managedBy: "platform" | "delegated";
  createdBy?: string;
}
export interface PrincipalOption {
  id: string;
  provider: string;
  displayName?: string;
  email?: string;
}
export interface RoleDefinition {
  id: string;
  displayName: string;
  description?: string;
  delegatable?: boolean;
  builtIn?: boolean;
  statements: Array<{ effect: "allow" | "deny"; capabilities: string[] }>;
}
export interface Policy {
  active: boolean;
  etag: number;
  revision: number;
  availableCapabilities: string[];
  builtInRoles: RoleDefinition[];
  spec: {
    version: number;
    roles?: RoleDefinition[];
    bindings: Assignment[];
  };
}
export interface DelegationState {
  namespace: string;
  etag: number;
  revision: number;
  bindings: Assignment[];
  roles: RoleDefinition[];
}
export interface ListResponse {
  items: Record<string, unknown>[];
  nextCursor?: string;
}
