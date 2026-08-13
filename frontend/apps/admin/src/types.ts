export type ViewKey = "overview" | "policy" | "providers" | "users" | "security" | "principals" | "sessions" | "tasks" | "relays" | "audit";
export interface Scope { namespace: string; capabilities?: string[] }
export interface Capabilities { capabilities: string[]; namespaceScopes: Scope[]; authenticationType: string; policyRevision: number }
export interface Bootstrap {
  identity: { id: string; groups: string[] };
  session: { authenticationType: string; createdAt: string; lastSeenAt: string; idleExpiresAt: string; absoluteExpiresAt: string };
  authorization: { capabilities: string[]; namespaceScopes: Scope[]; policyRevision: number; policyEtag: number };
}
export interface Overview {
  generatedAt: string;
  system: { controlPlane: { version: string; commit: string; protocolMin: string; protocolMax: string }; storage: { status: string; backend: string; schemaVersion: number } };
  security: { authenticationType: string; policyRevision: number; policyEtag: number };
  runtime: { activeSessions: { count: number; truncated: boolean }; activeTasks: { count: number; truncated: boolean }; relays: { total: number; online: number; ready: number; draining: number; reservations: number } };
  recentAudit: Record<string, unknown>[];
}
export interface Assignment { id: string; role: string; subjects?: string[]; groups?: string[]; namespaces?: string[] }
export interface Policy { active: boolean; etag: number; revision: number; spec: { version: number; assignments: Assignment[] } }
export interface ListResponse { items: Record<string, unknown>[]; nextCursor?: string }
