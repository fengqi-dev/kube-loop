export type ViewKey =
  | "overview"
  | "users"
  | "groups"
  | "invitations"
  | "oauthClients"
  | "policies"
  | "sessions"
  | "grants"
  | "audit"
  | "runtime";

export interface Bootstrap {
  identity: {
    id: string;
    displayName: string;
    email?: string;
    type: "human" | "machine";
    groups: string[];
  };
  session: { authenticationType: string };
  authorization: { administrator: boolean; namespaces: string[] };
}
export interface ListResponse<T = Record<string, unknown>> {
  items: T[];
  nextCursor?: string;
}
export interface Organization {
  id: string;
  name: string;
  slug: string;
  status: string;
  createdAt: string;
  updatedAt: string;
}
export interface Group {
  id: string;
  organizationId: string;
  name: string;
  description: string;
  system: boolean;
  createdAt: string;
  updatedAt: string;
}
export interface Identity {
  id: string;
  type: "human" | "machine";
  displayName: string;
  primaryEmail?: string;
  status: string;
  groups?: string[];
}
export interface Invitation {
  id: string;
  organizationId: string;
  email: string;
  groupId: string;
  status: string;
  expiresAt: string;
}
export interface OAuthClient {
  id: string;
  organizationId?: string;
  name: string;
  public: boolean;
  redirectUris: string[];
  grantTypes: string[];
  scopes: string[];
  trusted: boolean;
  enabled: boolean;
  builtin: boolean;
  machineIdentityId?: string;
  updatedAt: string;
}

export interface Overview {
  generatedAt: string;
  system: {
    controlPlane: { version: string; commit: string; protocolMin: string; protocolMax: string };
    storage: { status: string; backend: string; schemaVersion: number };
  };
  security: { authenticationType: string };
  runtime: {
    activeSessions: { count: number; truncated: boolean };
    activeTasks: { count: number; truncated: boolean };
    relays: { total: number; online: number; ready: number; draining: number; reservations: number };
  };
  recentAudit: AuditEvent[];
}

export interface EffectiveAuthorization {
  identityId: string;
  groups: string[];
  administrator: boolean;
  namespaces: string[];
  capabilities: Array<{
    id: string;
    scope: "platform" | "organization" | "namespace";
    allowed: boolean;
    namespaces?: string[];
    matchingGroupIds?: string[];
  }>;
}

export interface RuntimeSession {
  id: string;
  identityId: string;
  deviceId: string;
  clusterId: string;
  namespace: string;
  state: string;
  generation: number;
  createdAt: string;
  updatedAt: string;
  lastHeartbeatAt: string;
  expiresAt: string;
}

export interface OAuthGrant {
  authorizationId: string;
  identityId: string;
  clientId: string;
  deviceId: string;
  scopes: string[];
  status: string;
  createdAt: string;
  expiresAt: string;
  revokedAt?: string;
}

export interface AuditEvent {
  id: string;
  identityId?: string;
  action: string;
  resourceType?: string;
  resourceId?: string;
  outcome: string;
  requestId: string;
  createdAt: string;
}

export interface RuntimeTask {
  id: string;
  identityId: string;
  sessionId: string;
  type: string;
  state: string;
  version: number;
  createdAt: string;
  updatedAt: string;
  expiresAt?: string;
}

export interface RelayStatus {
  relayId: string;
  state: string;
  desiredState: string;
  online: boolean;
  reservations: number;
  controlVersion: number;
  lastHeartbeatAt: string;
  leaseExpiresAt: string;
}
