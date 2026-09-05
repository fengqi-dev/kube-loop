CREATE TABLE identities (
 id TEXT PRIMARY KEY, type TEXT NOT NULL CHECK (type IN ('human', 'machine')),
 display_name TEXT NOT NULL DEFAULT '', primary_email TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL CHECK (status IN ('active', 'suspended', 'disabled')),
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX identities_email_idx ON identities(primary_email);

CREATE TABLE password_credentials (
 identity_id TEXT PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
 username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE sessions (
 id TEXT PRIMARY KEY,
 identity_id TEXT NOT NULL REFERENCES identities(id) ON DELETE CASCADE, device_id TEXT NOT NULL,
 cluster_id TEXT NOT NULL, namespace TEXT NOT NULL DEFAULT '', state TEXT NOT NULL,
	 generation INTEGER NOT NULL DEFAULT 1 CHECK (generation >= 1), network_spec_json TEXT NOT NULL,
	 network_spec_hash TEXT NOT NULL CHECK (length(network_spec_hash) = 64), created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 last_heartbeat_at TEXT NOT NULL DEFAULT '', expires_at TEXT NOT NULL
);
CREATE TABLE tasks (
 id TEXT PRIMARY KEY,
 identity_id TEXT NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
 session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE, type TEXT NOT NULL, state TEXT NOT NULL,
 spec_json TEXT NOT NULL, result_json TEXT, idempotency_key TEXT NOT NULL,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL, expires_at TEXT, UNIQUE (identity_id, idempotency_key)
);
CREATE TABLE resource_snapshots (
 id TEXT PRIMARY KEY,
 task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, kind TEXT NOT NULL, namespace TEXT NOT NULL DEFAULT '',
 name TEXT NOT NULL, data_json TEXT NOT NULL, created_at TEXT NOT NULL, UNIQUE (task_id, kind, namespace, name)
);
CREATE TABLE idempotency_records (
 scope TEXT NOT NULL, key TEXT NOT NULL,
 request_hash TEXT NOT NULL, resource_type TEXT NOT NULL, resource_id TEXT NOT NULL, response_json TEXT,
 created_at TEXT NOT NULL, expires_at TEXT NOT NULL, PRIMARY KEY (scope, key)
);
CREATE TABLE audit_events (
 id TEXT PRIMARY KEY,
 identity_id TEXT REFERENCES identities(id) ON DELETE SET NULL, action TEXT NOT NULL,
 resource_type TEXT NOT NULL DEFAULT '', resource_id TEXT NOT NULL DEFAULT '', outcome TEXT NOT NULL,
 request_id TEXT NOT NULL, metadata_json TEXT, created_at TEXT NOT NULL
);

CREATE TABLE bootstrap_tokens (
 id INTEGER PRIMARY KEY CHECK (id = 1), token_hash BLOB NOT NULL UNIQUE CHECK (length(token_hash) = 32), created_at TEXT NOT NULL,
 expires_at TEXT NOT NULL, consumed_at TEXT
);
CREATE TABLE admin_sessions (
 id_hash BLOB PRIMARY KEY CHECK (length(id_hash) = 32),
 identity_id TEXT REFERENCES identities(id) ON DELETE CASCADE, authorization_id TEXT,
 authentication_type TEXT NOT NULL CHECK (authentication_type = 'normal'),
 csrf_token_hash BLOB NOT NULL CHECK (length(csrf_token_hash) = 32),
 authenticated_at TEXT NOT NULL, created_at TEXT NOT NULL, last_seen_at TEXT NOT NULL,
 idle_expires_at TEXT NOT NULL, absolute_expires_at TEXT NOT NULL, revoked_at TEXT
);

CREATE TABLE oauth_clients (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL, public INTEGER NOT NULL CHECK (public IN (0, 1)), redirect_uris_json TEXT NOT NULL,
 grant_types_json TEXT NOT NULL, scopes_json TEXT NOT NULL,
 trusted INTEGER NOT NULL DEFAULT 0 CHECK (trusted IN (0, 1)), enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
 builtin INTEGER NOT NULL DEFAULT 0 CHECK (builtin IN (0, 1)),
 machine_identity_id TEXT REFERENCES identities(id) ON DELETE RESTRICT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE oauth_client_secrets (
 client_id TEXT PRIMARY KEY REFERENCES oauth_clients(id) ON DELETE CASCADE,
 secret_hash BLOB NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE oauth_authorization_requests (
 challenge_hash BLOB PRIMARY KEY CHECK (length(challenge_hash) = 32),
 upstream_state_hash BLOB UNIQUE CHECK (upstream_state_hash IS NULL OR length(upstream_state_hash) = 32),
 request_id TEXT NOT NULL UNIQUE, request_json TEXT NOT NULL, csrf_hash BLOB NOT NULL CHECK (length(csrf_hash) = 32),
 identity_id TEXT REFERENCES identities(id) ON DELETE CASCADE, provider_id TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL CHECK (status IN ('pending', 'accepted', 'rejected', 'consumed')),
 created_at TEXT NOT NULL, expires_at TEXT NOT NULL
);
CREATE TABLE oauth_sessions (
 kind TEXT NOT NULL CHECK (kind IN ('authorization_code', 'pkce', 'oidc', 'access_token', 'refresh_token')),
 signature_hash BLOB NOT NULL CHECK (length(signature_hash) = 32), request_id TEXT NOT NULL,
 identity_id TEXT REFERENCES identities(id) ON DELETE CASCADE, client_id TEXT NOT NULL DEFAULT '', device_id TEXT NOT NULL DEFAULT '',
 request_json TEXT NOT NULL, status TEXT NOT NULL CHECK (status IN ('active', 'consumed', 'revoked')),
 created_at TEXT NOT NULL, expires_at TEXT NOT NULL, revoked_at TEXT, PRIMARY KEY (kind, signature_hash)
);
CREATE TABLE oauth_consents (
 identity_id TEXT NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
 client_id TEXT NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
 scope_hash BLOB NOT NULL CHECK (length(scope_hash) = 32), scopes_json TEXT NOT NULL,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY (identity_id, client_id, scope_hash)
);
CREATE TABLE oauth_browser_sessions (
 id_hash BLOB PRIMARY KEY CHECK (length(id_hash) = 32),
 identity_id TEXT NOT NULL REFERENCES identities(id) ON DELETE CASCADE, provider_id TEXT NOT NULL,
 auth_time TEXT NOT NULL, created_at TEXT NOT NULL, expires_at TEXT NOT NULL, revoked_at TEXT
);

CREATE INDEX sessions_identity_idx ON sessions(identity_id, updated_at);
CREATE INDEX sessions_expiry_idx ON sessions(expires_at);
CREATE INDEX sessions_namespace_state_idx ON sessions(namespace, state, updated_at);
CREATE INDEX tasks_session_idx ON tasks(session_id, updated_at);
CREATE INDEX idempotency_expiry_idx ON idempotency_records(expires_at);
CREATE INDEX audit_events_created_idx ON audit_events(created_at);
CREATE INDEX audit_events_identity_idx ON audit_events(identity_id, created_at);
CREATE INDEX admin_sessions_identity_idx ON admin_sessions(identity_id, absolute_expires_at);
CREATE INDEX admin_sessions_authorization_idx ON admin_sessions(authorization_id, revoked_at);
CREATE INDEX admin_sessions_expiry_idx ON admin_sessions(idle_expires_at, absolute_expires_at);
CREATE INDEX oauth_authorization_requests_expiry_idx ON oauth_authorization_requests(status, expires_at);
CREATE INDEX oauth_sessions_request_idx ON oauth_sessions(request_id, status);
CREATE INDEX oauth_sessions_identity_idx ON oauth_sessions(identity_id, status);
CREATE INDEX oauth_sessions_expiry_idx ON oauth_sessions(status, expires_at);
CREATE INDEX oauth_browser_sessions_identity_idx ON oauth_browser_sessions(identity_id, expires_at);
