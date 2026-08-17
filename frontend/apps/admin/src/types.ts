export type ViewKey = "users" | "oauthClients";

export interface Bootstrap {
  identity: {
    id: string;
    displayName: string;
    email?: string;
    type: "human" | "machine";
  };
  session: { authenticationType: string };
}
export interface ListResponse<T = Record<string, unknown>> {
  items: T[];
  nextCursor?: string;
}
export interface LocalUser {
  identityId: string;
  username: string;
  displayName: string;
  email?: string;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}
export interface OAuthClient {
  id: string;
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
