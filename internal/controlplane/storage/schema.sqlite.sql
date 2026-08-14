CREATE TABLE principals (
 id TEXT PRIMARY KEY, schema_version INTEGER NOT NULL CHECK (schema_version >= 1), provider TEXT NOT NULL,
 external_id TEXT NOT NULL, display_name TEXT NOT NULL DEFAULT '', email TEXT NOT NULL DEFAULT '',
 groups_json TEXT NOT NULL DEFAULT '[]', created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 UNIQUE (provider, external_id)
);
CREATE TABLE sessions (
 id TEXT PRIMARY KEY, schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
 principal_id TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE, device_id TEXT NOT NULL,
 cluster_id TEXT NOT NULL, namespace TEXT NOT NULL DEFAULT '', state TEXT NOT NULL,
 generation INTEGER NOT NULL DEFAULT 1 CHECK (generation >= 1), network_spec_json TEXT NOT NULL DEFAULT '{}',
 network_spec_hash TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 last_heartbeat_at TEXT NOT NULL DEFAULT '', expires_at TEXT NOT NULL
);
CREATE TABLE tasks (
 id TEXT PRIMARY KEY, schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
 principal_id TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
 session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE, type TEXT NOT NULL, state TEXT NOT NULL,
 spec_json TEXT NOT NULL, result_json TEXT, idempotency_key TEXT NOT NULL,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL, expires_at TEXT, UNIQUE (principal_id, idempotency_key)
);
CREATE TABLE resource_snapshots (
 id TEXT PRIMARY KEY, schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
 task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, kind TEXT NOT NULL, namespace TEXT NOT NULL DEFAULT '',
 name TEXT NOT NULL, data_json TEXT NOT NULL, created_at TEXT NOT NULL, UNIQUE (task_id, kind, namespace, name)
);
CREATE TABLE idempotency_records (
 schema_version INTEGER NOT NULL CHECK (schema_version >= 1), scope TEXT NOT NULL, key TEXT NOT NULL,
 request_hash TEXT NOT NULL, resource_type TEXT NOT NULL, resource_id TEXT NOT NULL, response_json TEXT,
 created_at TEXT NOT NULL, expires_at TEXT NOT NULL, PRIMARY KEY (scope, key)
);
CREATE TABLE audit_events (
 id TEXT PRIMARY KEY, schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
 principal_id TEXT REFERENCES principals(id) ON DELETE SET NULL, action TEXT NOT NULL,
 resource_type TEXT NOT NULL DEFAULT '', resource_id TEXT NOT NULL DEFAULT '', outcome TEXT NOT NULL,
 request_id TEXT NOT NULL, metadata_json TEXT, created_at TEXT NOT NULL
);
CREATE TABLE management_metadata (
 id INTEGER PRIMARY KEY CHECK (id = 1), bootstrap_retired_at TEXT,
 updated_at TEXT NOT NULL
);
CREATE TABLE admin_sessions (
 id_hash BLOB PRIMARY KEY CHECK (length(id_hash) = 32), schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
 principal_id TEXT REFERENCES principals(id) ON DELETE CASCADE, authorization_id TEXT,
 authentication_type TEXT NOT NULL CHECK (authentication_type IN ('normal', 'bootstrap', 'break-glass')),
 break_glass_generation TEXT NOT NULL DEFAULT '', csrf_token_hash BLOB NOT NULL CHECK (length(csrf_token_hash) = 32),
 created_at TEXT NOT NULL, last_seen_at TEXT NOT NULL, idle_expires_at TEXT NOT NULL,
 absolute_expires_at TEXT NOT NULL, revoked_at TEXT,
 CHECK ((authentication_type = 'break-glass' AND principal_id IS NULL AND authorization_id IS NULL AND break_glass_generation <> '') OR
 (authentication_type IN ('normal', 'bootstrap') AND principal_id IS NOT NULL AND authorization_id IS NOT NULL AND break_glass_generation = ''))
);
CREATE TABLE authorization_policies (
 id TEXT PRIMARY KEY,
 schema_version INTEGER NOT NULL CHECK (schema_version >= 1), spec_json TEXT NOT NULL,
 spec_hash TEXT NOT NULL CHECK (length(spec_hash) = 64),
 validation_state TEXT NOT NULL CHECK (validation_state IN ('draft', 'valid', 'invalid')), validation_json TEXT,
 created_by TEXT NOT NULL, created_authentication_type TEXT NOT NULL CHECK (created_authentication_type IN ('normal', 'bootstrap', 'break-glass')),
 reason TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE authorization_roles (
 policy_id TEXT NOT NULL REFERENCES authorization_policies(id) ON DELETE RESTRICT,
 id TEXT NOT NULL, definition_json TEXT NOT NULL,
 PRIMARY KEY (policy_id, id)
);
CREATE TABLE authorization_bindings (
 policy_id TEXT NOT NULL REFERENCES authorization_policies(id) ON DELETE RESTRICT,
 id TEXT NOT NULL, role_id TEXT NOT NULL, subject_type TEXT NOT NULL CHECK (subject_type IN ('principal', 'group')),
 principal_id TEXT, provider_id TEXT, group_name TEXT, scope_type TEXT NOT NULL CHECK (scope_type IN ('platform', 'namespaces')),
 namespace_names_json TEXT NOT NULL, label_selectors_json TEXT NOT NULL,
 managed_by TEXT NOT NULL CHECK (managed_by IN ('platform', 'delegated')), created_by TEXT NOT NULL,
 binding_json TEXT NOT NULL,
 PRIMARY KEY (policy_id, id),
 FOREIGN KEY (policy_id, role_id) REFERENCES authorization_roles(policy_id, id) ON DELETE RESTRICT,
 CHECK ((subject_type = 'principal' AND principal_id IS NOT NULL AND provider_id IS NULL AND group_name IS NULL) OR
 (subject_type = 'group' AND principal_id IS NULL AND provider_id IS NOT NULL AND group_name IS NOT NULL))
);
CREATE TABLE provider_configs (
 id TEXT PRIMARY KEY,
 schema_version INTEGER NOT NULL CHECK (schema_version >= 1), provider_id TEXT NOT NULL,
 provider_type TEXT NOT NULL CHECK (provider_type = 'oidc'), config_json TEXT NOT NULL,
 config_hash TEXT NOT NULL CHECK (length(config_hash) = 64), secret_aliases_json TEXT NOT NULL,
 validation_state TEXT NOT NULL CHECK (validation_state IN ('draft', 'valid', 'invalid')), validation_json TEXT,
 created_by TEXT NOT NULL, created_authentication_type TEXT NOT NULL CHECK (created_authentication_type IN ('normal', 'bootstrap', 'break-glass')),
 reason TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE management_active_configs (
 configuration_type TEXT NOT NULL CHECK (configuration_type IN ('policy', 'provider')),
 configuration_id TEXT NOT NULL, object_id TEXT NOT NULL,
 updated_by TEXT NOT NULL, updated_authentication_type TEXT NOT NULL CHECK (updated_authentication_type IN ('normal', 'bootstrap', 'break-glass')),
 updated_at TEXT NOT NULL, PRIMARY KEY (configuration_type, configuration_id)
);
CREATE TABLE config_change_requests (
 id TEXT PRIMARY KEY, schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
 configuration_type TEXT NOT NULL CHECK (configuration_type IN ('policy', 'provider')), configuration_id TEXT NOT NULL,
 base_object_id TEXT, proposed_object_id TEXT NOT NULL,
 status TEXT NOT NULL CHECK (status IN ('draft', 'validated', 'published', 'rejected')),
 idempotency_hash BLOB NOT NULL CHECK (length(idempotency_hash) = 32), request_hash TEXT NOT NULL CHECK (length(request_hash) = 64),
 requested_by TEXT NOT NULL, requested_authentication_type TEXT NOT NULL CHECK (requested_authentication_type IN ('normal', 'bootstrap', 'break-glass')),
 reason TEXT NOT NULL, validation_json TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 UNIQUE (requested_by, requested_authentication_type, configuration_type, configuration_id, idempotency_hash)
);
CREATE TABLE relay_desired_states (
 relay_id TEXT PRIMARY KEY, schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
 desired_state TEXT NOT NULL CHECK (desired_state IN ('ready', 'draining')), version INTEGER NOT NULL CHECK (version > 0),
 updated_by TEXT NOT NULL, updated_authentication_type TEXT NOT NULL CHECK (updated_authentication_type IN ('normal', 'bootstrap', 'break-glass')),
 reason TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE audit_export_jobs (
 id TEXT PRIMARY KEY, schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
 state TEXT NOT NULL CHECK (state IN ('pending', 'running', 'succeeded', 'failed')), filter_json TEXT NOT NULL,
 result_data TEXT NOT NULL DEFAULT '', error_code TEXT NOT NULL DEFAULT '', requested_by TEXT NOT NULL,
 requested_authentication_type TEXT NOT NULL CHECK (requested_authentication_type IN ('normal', 'bootstrap', 'break-glass')),
 reason TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, expires_at TEXT NOT NULL
);
CREATE TABLE local_admin_users (
 principal_id TEXT PRIMARY KEY REFERENCES principals(id) ON DELETE CASCADE,
 schema_version INTEGER NOT NULL CHECK (schema_version >= 1), username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)), totp_secret_encrypted BLOB,
 bootstrap_complete INTEGER NOT NULL DEFAULT 0 CHECK (bootstrap_complete IN (0, 1)),
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE admin_recovery_codes (
 principal_id TEXT NOT NULL REFERENCES local_admin_users(principal_id) ON DELETE CASCADE,
 code_hash BLOB NOT NULL CHECK (length(code_hash) = 32), created_at TEXT NOT NULL,
 PRIMARY KEY (principal_id, code_hash)
);
CREATE TABLE oauth_clients (
 id TEXT PRIMARY KEY, schema_version INTEGER NOT NULL CHECK (schema_version >= 1), name TEXT NOT NULL,
 public INTEGER NOT NULL CHECK (public IN (0, 1)), redirect_uris_json TEXT NOT NULL, grant_types_json TEXT NOT NULL,
 response_types_json TEXT NOT NULL, scopes_json TEXT NOT NULL,
 trusted INTEGER NOT NULL DEFAULT 0 CHECK (trusted IN (0, 1)), enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
 builtin INTEGER NOT NULL DEFAULT 0 CHECK (builtin IN (0, 1)),
 machine_principal_id TEXT REFERENCES principals(id) ON DELETE RESTRICT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE oauth_client_secrets (
 client_id TEXT PRIMARY KEY REFERENCES oauth_clients(id) ON DELETE CASCADE,
 schema_version INTEGER NOT NULL CHECK (schema_version >= 1), secret_hash BLOB NOT NULL,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE oauth_authorization_requests (
 challenge_hash BLOB PRIMARY KEY CHECK (length(challenge_hash) = 32),
 upstream_state_hash BLOB UNIQUE CHECK (upstream_state_hash IS NULL OR length(upstream_state_hash) = 32),
 request_id TEXT NOT NULL UNIQUE, request_json TEXT NOT NULL, csrf_hash BLOB NOT NULL CHECK (length(csrf_hash) = 32),
 principal_id TEXT REFERENCES principals(id) ON DELETE CASCADE, provider_id TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL CHECK (status IN ('pending', 'accepted', 'rejected', 'consumed')),
 created_at TEXT NOT NULL, expires_at TEXT NOT NULL
);
CREATE TABLE oauth_sessions (
 kind TEXT NOT NULL CHECK (kind IN ('authorization_code', 'pkce', 'oidc', 'access_token', 'refresh_token')),
 signature_hash BLOB NOT NULL CHECK (length(signature_hash) = 32), request_id TEXT NOT NULL,
 principal_id TEXT REFERENCES principals(id) ON DELETE CASCADE, client_id TEXT NOT NULL DEFAULT '', device_id TEXT NOT NULL DEFAULT '',
 request_json TEXT NOT NULL, status TEXT NOT NULL CHECK (status IN ('active', 'consumed', 'revoked')),
 created_at TEXT NOT NULL, expires_at TEXT NOT NULL, revoked_at TEXT, PRIMARY KEY (kind, signature_hash)
);
CREATE TABLE oauth_consents (
 principal_id TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
 client_id TEXT NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
 scope_hash BLOB NOT NULL CHECK (length(scope_hash) = 32), scopes_json TEXT NOT NULL,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY (principal_id, client_id, scope_hash)
);
CREATE TABLE oauth_browser_sessions (
 id_hash BLOB PRIMARY KEY CHECK (length(id_hash) = 32),
 principal_id TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE, provider_id TEXT NOT NULL,
 auth_time TEXT NOT NULL, created_at TEXT NOT NULL, expires_at TEXT NOT NULL, revoked_at TEXT
);
CREATE INDEX sessions_principal_idx ON sessions(principal_id, updated_at);
CREATE INDEX sessions_expiry_idx ON sessions(expires_at);
CREATE INDEX sessions_namespace_state_idx ON sessions(namespace, state, updated_at);
CREATE INDEX tasks_session_idx ON tasks(session_id, updated_at);
CREATE INDEX idempotency_expiry_idx ON idempotency_records(expires_at);
CREATE INDEX audit_events_created_idx ON audit_events(created_at);
CREATE INDEX audit_events_principal_idx ON audit_events(principal_id, created_at);
CREATE INDEX admin_sessions_principal_idx ON admin_sessions(principal_id, absolute_expires_at);
CREATE INDEX admin_sessions_authorization_idx ON admin_sessions(authorization_id, revoked_at);
CREATE INDEX admin_sessions_expiry_idx ON admin_sessions(idle_expires_at, absolute_expires_at);
CREATE INDEX provider_configs_provider_idx ON provider_configs(provider_id, created_at DESC);
CREATE INDEX authorization_policies_created_idx ON authorization_policies(created_at, id);
CREATE INDEX authorization_bindings_subject_idx ON authorization_bindings(subject_type, principal_id, provider_id, group_name, policy_id);
CREATE INDEX authorization_bindings_role_idx ON authorization_bindings(policy_id, role_id, id);
CREATE INDEX config_change_requests_status_idx ON config_change_requests(status, updated_at, id);
CREATE INDEX audit_export_jobs_pending_idx ON audit_export_jobs(state, created_at, id);
CREATE INDEX oauth_authorization_requests_expiry_idx ON oauth_authorization_requests(status, expires_at);
CREATE INDEX oauth_sessions_request_idx ON oauth_sessions(request_id, status);
CREATE INDEX oauth_sessions_principal_idx ON oauth_sessions(principal_id, status);
CREATE INDEX oauth_sessions_expiry_idx ON oauth_sessions(status, expires_at);
CREATE INDEX oauth_browser_sessions_principal_idx ON oauth_browser_sessions(principal_id, expires_at);
