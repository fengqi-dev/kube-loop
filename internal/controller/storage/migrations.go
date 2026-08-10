package storage

type migration struct {
	version    int
	sqlite     []string
	postgresql []string
}

var migrations = []migration{{
	version: 1,
	sqlite: []string{
		`CREATE TABLE principals (
			id TEXT PRIMARY KEY,
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			provider TEXT NOT NULL,
			external_id TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL DEFAULT '',
			groups_json TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE (provider, external_id)
		)`,
		`CREATE TABLE token_families (
			id TEXT PRIMARY KEY,
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			principal_id TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
			device_id TEXT NOT NULL,
			refresh_token_hash BLOB NOT NULL UNIQUE,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			revoked_at TEXT
		)`,
		`CREATE INDEX token_families_expiry_idx ON token_families(expires_at)`,
		`CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			principal_id TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
			device_id TEXT NOT NULL,
			cluster_id TEXT NOT NULL,
			state TEXT NOT NULL,
			generation INTEGER NOT NULL DEFAULT 1 CHECK (generation >= 1),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			expires_at TEXT NOT NULL
		)`,
		`CREATE INDEX sessions_principal_idx ON sessions(principal_id, updated_at)`,
		`CREATE INDEX sessions_expiry_idx ON sessions(expires_at)`,
		`CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			principal_id TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			type TEXT NOT NULL,
			state TEXT NOT NULL,
			spec_json TEXT NOT NULL,
			result_json TEXT,
			idempotency_key TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			expires_at TEXT,
			UNIQUE (principal_id, idempotency_key)
		)`,
		`CREATE INDEX tasks_session_idx ON tasks(session_id, updated_at)`,
		`CREATE TABLE resource_snapshots (
			id TEXT PRIMARY KEY,
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			kind TEXT NOT NULL,
			namespace TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL,
			data_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE (task_id, kind, namespace, name)
		)`,
		`CREATE TABLE idempotency_records (
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			scope TEXT NOT NULL,
			key TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			resource_type TEXT NOT NULL,
			resource_id TEXT NOT NULL,
			response_json TEXT,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			PRIMARY KEY (scope, key)
		)`,
		`CREATE INDEX idempotency_expiry_idx ON idempotency_records(expires_at)`,
		`CREATE TABLE audit_events (
			id TEXT PRIMARY KEY,
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			principal_id TEXT REFERENCES principals(id) ON DELETE SET NULL,
			action TEXT NOT NULL,
			resource_type TEXT NOT NULL DEFAULT '',
			resource_id TEXT NOT NULL DEFAULT '',
			outcome TEXT NOT NULL,
			request_id TEXT NOT NULL,
			metadata_json TEXT,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX audit_events_created_idx ON audit_events(created_at)`,
		`CREATE INDEX audit_events_principal_idx ON audit_events(principal_id, created_at)`,
	},
	postgresql: []string{
		`CREATE TABLE principals (
			id TEXT PRIMARY KEY,
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			provider TEXT NOT NULL,
			external_id TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL DEFAULT '',
			groups_json JSONB NOT NULL DEFAULT '[]'::jsonb,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE (provider, external_id)
		)`,
		`CREATE TABLE token_families (
			id TEXT PRIMARY KEY,
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			principal_id TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
			device_id TEXT NOT NULL,
			refresh_token_hash BYTEA NOT NULL UNIQUE,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			revoked_at TEXT
		)`,
		`CREATE INDEX token_families_expiry_idx ON token_families(expires_at)`,
		`CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			principal_id TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
			device_id TEXT NOT NULL,
			cluster_id TEXT NOT NULL,
			state TEXT NOT NULL,
			generation BIGINT NOT NULL DEFAULT 1 CHECK (generation >= 1),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			expires_at TEXT NOT NULL
		)`,
		`CREATE INDEX sessions_principal_idx ON sessions(principal_id, updated_at)`,
		`CREATE INDEX sessions_expiry_idx ON sessions(expires_at)`,
		`CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			principal_id TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			type TEXT NOT NULL,
			state TEXT NOT NULL,
			spec_json JSONB NOT NULL,
			result_json JSONB,
			idempotency_key TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			expires_at TEXT,
			UNIQUE (principal_id, idempotency_key)
		)`,
		`CREATE INDEX tasks_session_idx ON tasks(session_id, updated_at)`,
		`CREATE TABLE resource_snapshots (
			id TEXT PRIMARY KEY,
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			kind TEXT NOT NULL,
			namespace TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL,
			data_json JSONB NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE (task_id, kind, namespace, name)
		)`,
		`CREATE TABLE idempotency_records (
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			scope TEXT NOT NULL,
			key TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			resource_type TEXT NOT NULL,
			resource_id TEXT NOT NULL,
			response_json JSONB,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			PRIMARY KEY (scope, key)
		)`,
		`CREATE INDEX idempotency_expiry_idx ON idempotency_records(expires_at)`,
		`CREATE TABLE audit_events (
			id TEXT PRIMARY KEY,
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			principal_id TEXT REFERENCES principals(id) ON DELETE SET NULL,
			action TEXT NOT NULL,
			resource_type TEXT NOT NULL DEFAULT '',
			resource_id TEXT NOT NULL DEFAULT '',
			outcome TEXT NOT NULL,
			request_id TEXT NOT NULL,
			metadata_json JSONB,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX audit_events_created_idx ON audit_events(created_at)`,
		`CREATE INDEX audit_events_principal_idx ON audit_events(principal_id, created_at)`,
	},
}, {
	version: 2,
	sqlite: []string{
		`CREATE TABLE auth_attempts (
			id TEXT PRIMARY KEY,
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			provider_id TEXT NOT NULL,
			state_hash BLOB NOT NULL UNIQUE,
			client_state TEXT NOT NULL,
			client_callback TEXT NOT NULL,
			nonce TEXT NOT NULL,
			pkce_challenge TEXT NOT NULL,
			upstream_pkce_verifier TEXT NOT NULL,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL
		)`,
		`CREATE INDEX auth_attempts_expiry_idx ON auth_attempts(expires_at)`,
		`CREATE TABLE auth_exchanges (
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			code_hash BLOB PRIMARY KEY,
			principal_id TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
			provider_id TEXT NOT NULL,
			pkce_challenge TEXT NOT NULL,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL
		)`,
		`CREATE INDEX auth_exchanges_expiry_idx ON auth_exchanges(expires_at)`,
	},
	postgresql: []string{
		`CREATE TABLE auth_attempts (
			id TEXT PRIMARY KEY,
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			provider_id TEXT NOT NULL,
			state_hash BYTEA NOT NULL UNIQUE,
			client_state TEXT NOT NULL,
			client_callback TEXT NOT NULL,
			nonce TEXT NOT NULL,
			pkce_challenge TEXT NOT NULL,
			upstream_pkce_verifier TEXT NOT NULL,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL
		)`,
		`CREATE INDEX auth_attempts_expiry_idx ON auth_attempts(expires_at)`,
		`CREATE TABLE auth_exchanges (
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			code_hash BYTEA PRIMARY KEY,
			principal_id TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
			provider_id TEXT NOT NULL,
			pkce_challenge TEXT NOT NULL,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL
		)`,
		`CREATE INDEX auth_exchanges_expiry_idx ON auth_exchanges(expires_at)`,
	},
}, {
	version: 3,
	sqlite: []string{
		`CREATE TABLE refresh_tokens (
			token_hash BLOB PRIMARY KEY,
			family_id TEXT NOT NULL REFERENCES token_families(id) ON DELETE CASCADE,
			status TEXT NOT NULL CHECK (status IN ('active', 'used')),
			created_at TEXT NOT NULL,
			used_at TEXT
		)`,
		`CREATE INDEX refresh_tokens_family_idx ON refresh_tokens(family_id, created_at)`,
	},
	postgresql: []string{
		`CREATE TABLE refresh_tokens (
			token_hash BYTEA PRIMARY KEY,
			family_id TEXT NOT NULL REFERENCES token_families(id) ON DELETE CASCADE,
			status TEXT NOT NULL CHECK (status IN ('active', 'used')),
			created_at TEXT NOT NULL,
			used_at TEXT
		)`,
		`CREATE INDEX refresh_tokens_family_idx ON refresh_tokens(family_id, created_at)`,
	},
}, {
	version: 4,
	sqlite: []string{
		`ALTER TABLE sessions ADD COLUMN namespace TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN last_heartbeat_at TEXT NOT NULL DEFAULT ''`,
		`UPDATE sessions SET namespace = cluster_id WHERE namespace = ''`,
		`UPDATE sessions SET last_heartbeat_at = updated_at WHERE last_heartbeat_at = ''`,
		`CREATE INDEX sessions_namespace_state_idx ON sessions(namespace, state, updated_at)`,
	},
	postgresql: []string{
		`ALTER TABLE sessions ADD COLUMN namespace TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN last_heartbeat_at TEXT NOT NULL DEFAULT ''`,
		`UPDATE sessions SET namespace = cluster_id WHERE namespace = ''`,
		`UPDATE sessions SET last_heartbeat_at = updated_at WHERE last_heartbeat_at = ''`,
		`CREATE INDEX sessions_namespace_state_idx ON sessions(namespace, state, updated_at)`,
	},
}, {
	version: 5,
	sqlite: []string{
		`ALTER TABLE sessions ADD COLUMN network_spec_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE sessions ADD COLUMN network_spec_hash TEXT NOT NULL DEFAULT ''`,
		`UPDATE sessions SET state = 'expired' WHERE state = 'active'`,
	},
	postgresql: []string{
		`ALTER TABLE sessions ADD COLUMN network_spec_json JSONB NOT NULL DEFAULT '{}'::jsonb`,
		`ALTER TABLE sessions ADD COLUMN network_spec_hash TEXT NOT NULL DEFAULT ''`,
		`UPDATE sessions SET state = 'expired' WHERE state = 'active'`,
	},
}, {
	version: 6,
	sqlite: []string{
		`UPDATE tasks SET state = 'running' WHERE state = 'active'`,
		`UPDATE tasks SET state = 'starting' WHERE state = 'preparing'`,
	},
	postgresql: []string{
		`UPDATE tasks SET state = 'running' WHERE state = 'active'`,
		`UPDATE tasks SET state = 'starting' WHERE state = 'preparing'`,
	},
}, {
	version: 7,
	sqlite: []string{
		`CREATE TABLE management_metadata (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			bootstrap_retired_at TEXT,
			bootstrap_retired_revision INTEGER CHECK (bootstrap_retired_revision IS NULL OR bootstrap_retired_revision >= 1),
			updated_at TEXT NOT NULL,
			CHECK ((bootstrap_retired_at IS NULL) = (bootstrap_retired_revision IS NULL))
		)`,
	},
	postgresql: []string{
		`CREATE TABLE management_metadata (
			id BIGINT PRIMARY KEY CHECK (id = 1),
			bootstrap_retired_at TEXT,
			bootstrap_retired_revision BIGINT CHECK (bootstrap_retired_revision IS NULL OR bootstrap_retired_revision >= 1),
			updated_at TEXT NOT NULL,
			CHECK ((bootstrap_retired_at IS NULL) = (bootstrap_retired_revision IS NULL))
		)`,
	},
}, {
	version: 8,
	sqlite: []string{
		`CREATE TABLE admin_sessions (
			id_hash BLOB PRIMARY KEY CHECK (length(id_hash) = 32),
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			principal_id TEXT REFERENCES principals(id) ON DELETE CASCADE,
			token_family_id TEXT REFERENCES token_families(id) ON DELETE CASCADE,
			authentication_type TEXT NOT NULL CHECK (authentication_type IN ('normal', 'bootstrap', 'break-glass')),
			break_glass_generation TEXT NOT NULL DEFAULT '',
			csrf_token_hash BLOB NOT NULL CHECK (length(csrf_token_hash) = 32),
			created_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL,
			idle_expires_at TEXT NOT NULL,
			absolute_expires_at TEXT NOT NULL,
			revoked_at TEXT,
			CHECK (
				(authentication_type = 'break-glass' AND principal_id IS NULL AND token_family_id IS NULL AND break_glass_generation <> '') OR
				(authentication_type IN ('normal', 'bootstrap') AND principal_id IS NOT NULL AND token_family_id IS NOT NULL AND break_glass_generation = '')
			)
		)`,
		`CREATE INDEX admin_sessions_principal_idx ON admin_sessions(principal_id, absolute_expires_at)`,
		`CREATE INDEX admin_sessions_expiry_idx ON admin_sessions(idle_expires_at, absolute_expires_at)`,
	},
	postgresql: []string{
		`CREATE TABLE admin_sessions (
			id_hash BYTEA PRIMARY KEY CHECK (octet_length(id_hash) = 32),
			schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
			principal_id TEXT REFERENCES principals(id) ON DELETE CASCADE,
			token_family_id TEXT REFERENCES token_families(id) ON DELETE CASCADE,
			authentication_type TEXT NOT NULL CHECK (authentication_type IN ('normal', 'bootstrap', 'break-glass')),
			break_glass_generation TEXT NOT NULL DEFAULT '',
			csrf_token_hash BYTEA NOT NULL CHECK (octet_length(csrf_token_hash) = 32),
			created_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL,
			idle_expires_at TEXT NOT NULL,
			absolute_expires_at TEXT NOT NULL,
			revoked_at TEXT,
			CHECK (
				(authentication_type = 'break-glass' AND principal_id IS NULL AND token_family_id IS NULL AND break_glass_generation <> '') OR
				(authentication_type IN ('normal', 'bootstrap') AND principal_id IS NOT NULL AND token_family_id IS NOT NULL AND break_glass_generation = '')
			)
		)`,
		`CREATE INDEX admin_sessions_principal_idx ON admin_sessions(principal_id, absolute_expires_at)`,
		`CREATE INDEX admin_sessions_expiry_idx ON admin_sessions(idle_expires_at, absolute_expires_at)`,
	},
}}

func currentSchemaVersion() int {
	return migrations[len(migrations)-1].version
}
