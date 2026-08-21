package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestSQLiteOpenInitializesAndConfiguresDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "kubeloop.db")
	store, err := Open(
		context.Background(),
		Config{Backend: BackendSQLite, SQLitePath: path},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if store.Backend() != BackendSQLite {
		t.Fatalf("backend = %q", store.Backend())
	}
	var schemaID string
	if err := store.db.QueryRow(`SELECT schema_id FROM schema_metadata WHERE id = 1`).Scan(&schemaID); err != nil ||
		schemaID != currentSchemaID {
		t.Fatalf("schema ID = %q, error = %v", schemaID, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != operatingSystemWindows && info.Mode().Perm() != 0o600 {
		t.Fatalf("database permissions = %o", info.Mode().Perm())
	}
	assertSQLitePragma(t, store, "foreign_keys", "1")
	assertSQLitePragma(t, store, "journal_mode", "wal")
	assertSQLitePragma(t, store, "synchronous", "1")
	assertSQLitePragma(t, store, "busy_timeout", "5000")
	if err := store.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteInitialSchemaOmitsRemovedResourcesAndLegacyFields(t *testing.T) {
	store := openSQLiteTestStore(t, filepath.Join(t.TempDir(), "initial.db"))
	for _, table := range []string{"identity_emails", "invitations", "organizations", "organization_memberships", "iam_groups", "group_memberships", "group_namespaces", "security_policies"} {
		var tableCount int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).
			Scan(&tableCount); err != nil {
			t.Fatal(err)
		}
		if tableCount != 0 {
			t.Fatalf("initial schema contains removed %s table", table)
		}
	}
	for table, omitted := range map[string][]string{
		"admin_sessions":       {"schema_version"},
		"audit_events":         {"schema_version"},
		"idempotency_records":  {"schema_version"},
		"relay_desired_states": {"schema_version"},
		"resource_snapshots":   {"schema_version"},
		"sessions":             {"schema_version"},
		"tasks":                {"schema_version"},
	} {
		columns := sqliteTableColumns(t, store.db, table)
		for _, column := range omitted {
			if _, exists := columns[column]; exists {
				t.Fatalf(
					"initial %s table contains legacy %s column",
					table,
					column,
				)
			}
		}
	}
}

func sqliteTableColumns(t *testing.T, database *sql.DB, table string) map[string]struct{} {
	t.Helper()
	rows, err := database.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	columns := make(map[string]struct{})
	for rows.Next() {
		var position, notNull, primaryKey int
		var name, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&position, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return columns
}

func TestSQLiteFileURLUsesPortableAbsoluteURI(t *testing.T) {
	if got, want := sqliteFileURL(
		`/var/lib/kubeloop/state.db`,
		false,
	), `file:///var/lib/kubeloop/state.db`; got != want {
		t.Fatalf("Unix SQLite URL = %q, want %q", got, want)
	}
	if got, want := sqliteFileURL(`D:\a\kube-loop\state.db`, true), `file:///D:/a/kube-loop/state.db`; got != want {
		t.Fatalf("Windows SQLite URL = %q, want %q", got, want)
	}
}

func TestSQLiteIdentityRepositoryPersistsIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeloop.db")
	store := openSQLiteTestStore(t, path)
	created := time.Date(2026, 8, 9, 1, 2, 3, 0, time.UTC)
	first, err := store.Identities().Create(context.Background(), Identity{
		ID: uuid.NewString(), Type: identityTypeHuman, DisplayName: "First Name", PrimaryEmail: "first@example.test",
		Status: statusActive, CreatedAt: created,
	})
	if err != nil {
		t.Fatal(err)
	}
	first.DisplayName = "Updated Name"
	first.PrimaryEmail = "updated@example.test"
	first.UpdatedAt = created.Add(2 * time.Hour)
	if err := store.Identities().Update(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	byID, err := store.Identities().GetByID(context.Background(), first.ID)
	if err != nil || byID.PrimaryEmail != "updated@example.test" ||
		byID.DisplayName != "Updated Name" {
		t.Fatalf("GetByID = %#v, %v", byID, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openSQLiteTestStore(t, path)
	persisted, err := store.Identities().GetByID(context.Background(), first.ID)
	if err != nil || persisted.ID != first.ID {
		t.Fatalf("persisted identity = %#v, %v", persisted, err)
	}
}

func TestSQLiteRejectsUnsupportedSchemaAndUnsafePaths(t *testing.T) {
	t.Run("unsupported schema", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "kubeloop.db")
		store := openSQLiteTestStore(t, path)
		if _, err := store.db.Exec(`UPDATE schema_metadata SET schema_id = 'legacy' WHERE id = 1`); err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(context.Background(), Config{Backend: BackendSQLite, SQLitePath: path}); err == nil ||
			!strings.Contains(err.Error(), "recreate") {
			t.Fatalf("Open unsupported schema error = %v", err)
		}
	})
	t.Run("symbolic link", func(t *testing.T) {
		if runtime.GOOS == operatingSystemWindows {
			t.Skip("symlink permissions vary on Windows")
		}
		directory := t.TempDir()
		target := filepath.Join(directory, "target.db")
		if err := os.WriteFile(target, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(directory, "link.db")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(context.Background(), Config{Backend: BackendSQLite, SQLitePath: link}); err == nil ||
			!strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("Open symlink error = %v", err)
		}
	})
	t.Run("corrupt database", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "kubeloop.db")
		if err := os.WriteFile(path, []byte("not a sqlite database"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(context.Background(), Config{Backend: BackendSQLite, SQLitePath: path}); err == nil {
			t.Fatal("Open accepted a corrupt SQLite database")
		}
	})
}

func TestSQLiteRejectsNonemptyUninitializedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeloop.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE identities (broken TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), Config{Backend: BackendSQLite, SQLitePath: path}); err == nil ||
		!strings.Contains(err.Error(), "recreate") {
		t.Fatalf("Open uninitialized database error = %v", err)
	}
	database, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	var metadataTableCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_metadata'`).
		Scan(&metadataTableCount); err != nil {
		t.Fatal(err)
	}
	if metadataTableCount != 0 {
		t.Fatal("rejected database gained schema metadata")
	}
}

func TestSQLiteRejectsReadOnlyDatabase(t *testing.T) {
	if runtime.GOOS == operatingSystemWindows {
		t.Skip("POSIX permission test")
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "kubeloop.db")
	store := openSQLiteTestStore(t, path)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(directory, 0o700)
		_ = os.Chmod(path, 0o600)
	})
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(
		context.Background(),
		Config{Backend: BackendSQLite, SQLitePath: path},
	)
	if err == nil {
		_ = reopened.Close()
		t.Skip(
			"current user can bypass read-only mode (usually a privileged test runner)",
		)
	}
}

func TestSQLiteDiskFullRollsBackWrite(t *testing.T) {
	store := openSQLiteTestStore(t, filepath.Join(t.TempDir(), "kubeloop.db"))
	var pages int
	if err := store.db.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(fmt.Sprintf(`PRAGMA max_page_count=%d`, pages+1)); err != nil {
		t.Fatal(err)
	}
	metadata := make([]byte, 2<<20)
	for index := range metadata {
		metadata[index] = 'x'
	}
	metadata[0] = '"'
	metadata[len(metadata)-1] = '"'
	eventID := uuid.NewString()
	err := store.Audit().Append(context.Background(), AuditEvent{
		ID: eventID, Action: "disk.full", Outcome: "failed", RequestID: "request-disk-full",
		Metadata: metadata, CreatedAt: time.Now(),
	})
	if err == nil {
		t.Fatal(
			"audit write unexpectedly succeeded with max_page_count exhausted",
		)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE id = ?`, eventID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed disk-full write left a partial audit event")
	}
}

func TestSQLiteRecoversAfterProcessExitDuringTransaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeloop.db")
	store := openSQLiteTestStore(t, path)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	identityID := uuid.NewString()
	command := exec.Command(os.Args[0], "-test.run=^TestSQLiteCrashHelper$")
	command.Env = append(os.Environ(),
		"KUBELOOP_SQLITE_CRASH_HELPER=1",
		"KUBELOOP_SQLITE_CRASH_PATH="+path,
		"KUBELOOP_SQLITE_CRASH_IDENTITY="+identityID,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("crash helper: %v: %s", err, output)
	}
	store = openSQLiteTestStore(t, path)
	if _, err := store.Identities().GetByID(context.Background(), identityID); !errors.Is(
		err,
		ErrNotFound,
	) {
		t.Fatalf("uncommitted identity survived process exit: %v", err)
	}
	if err := store.checkSQLiteIntegrity(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteCrashHelper(_ *testing.T) {
	if os.Getenv("KUBELOOP_SQLITE_CRASH_HELPER") != "1" {
		return
	}
	store, err := Open(context.Background(), Config{
		Backend: BackendSQLite, SQLitePath: os.Getenv("KUBELOOP_SQLITE_CRASH_PATH"),
	})
	if err != nil {
		os.Exit(2)
	}
	transaction, err := store.orm.BeginTx(context.Background(), nil)
	if err != nil {
		os.Exit(3)
	}
	repositories := newRepositorySet(BackendSQLite, transaction.Tx, transaction)
	_, err = repositories.Identities().Create(context.Background(), Identity{
		ID: os.Getenv(
			"KUBELOOP_SQLITE_CRASH_IDENTITY",
		), Type: identityTypeHuman, DisplayName: "Crash Helper", Status: statusActive,
	})
	if err != nil {
		os.Exit(4)
	}
	// Exit without Commit, Rollback or Close to simulate abrupt process loss.
	os.Exit(0)
}

func TestStorageConfigurationSecurity(t *testing.T) {
	if _, err := (Config{Backend: BackendSQLite, SQLitePath: "db", ControlPlaneReplicas: 2}).normalized(); err == nil {
		t.Fatal("SQLite accepted multiple Control Plane replicas")
	}
	if _, err := (Config{DatasourceURL: "postgres://user:secret@db.example/test?sslmode=disable"}).normalized(); err == nil {
		t.Fatal("PostgreSQL accepted disabled TLS")
	}
	if _, err := (Config{DatasourceURL: "postgres://user:secret@db.example/test?sslmode=prefer"}).normalized(); err == nil {
		t.Fatal("PostgreSQL accepted downgrade-capable preferred TLS")
	}
	if _, err := (Config{DatasourceURL: "postgres://user:secret@db.example/test?sslmode=require"}).normalized(); err != nil {
		t.Fatalf("PostgreSQL TLS config rejected: %v", err)
	}
	mysqlConfig, err := (Config{DatasourceURL: "mysql://user:secret@db.example/test?tls=true"}).normalized()
	if err != nil || mysqlConfig.Backend != BackendMySQL {
		t.Fatalf("MySQL datasource URL rejected: %#v, %v", mysqlConfig, err)
	}
	if _, err := (Config{DatasourceURL: "mysql://user:secret@db.example/test?tls=false"}).normalized(); err == nil {
		t.Fatal("MySQL accepted disabled TLS")
	}
	if _, err := (Config{DatasourceURL: "mariadb://user:secret@db.example/test"}).normalized(); err == nil {
		t.Fatal("unsupported datasource scheme was accepted")
	}
	redacted := RedactedDatasourceURL(
		"postgres://user:secret@db.example/test?sslmode=require",
	)
	if strings.Contains(redacted, "secret") ||
		!strings.Contains(redacted, "REDACTED") {
		t.Fatalf("redacted DSN = %q", redacted)
	}
	redacted = RedactedDatasourceURL(
		"host=db.example user=app password=secret sslmode=require",
	)
	if strings.Contains(redacted, "secret") ||
		!strings.Contains(redacted, "REDACTED") {
		t.Fatalf("keyword DSN was not redacted: %q", redacted)
	}
	redacted = RedactedDatasourceURL(
		"host=db.example user=app password='secret with spaces' sslmode=require",
	)
	if strings.Contains(redacted, "secret with spaces") ||
		!strings.Contains(redacted, "REDACTED") {
		t.Fatalf("quoted keyword DSN was not fully redacted: %q", redacted)
	}
	redacted = RedactedDatasourceURL(
		"postgres://user@db.example/test?sslmode=require&password=query-secret",
	)
	if strings.Contains(redacted, "query-secret") ||
		!strings.Contains(redacted, "REDACTED") {
		t.Fatalf("URL query password was not redacted: %q", redacted)
	}
}

func TestMySQLSchemaDialectConversion(t *testing.T) {
	converted := strings.Builder{}
	for _, statement := range schemaStatements(BackendMySQL) {
		converted.WriteString(statement)
		for _, forbidden := range []string{"AUTOINCREMENT", " BLOB", "idempotency_`key`"} {
			if strings.Contains(statement, forbidden) {
				t.Fatalf("MySQL schema contains %q: %s", forbidden, statement)
			}
		}
	}
	for _, omitted := range []string{"schema_version", "identity_emails"} {
		if strings.Contains(converted.String(), omitted) {
			t.Fatalf("MySQL schema contains legacy schema item %q", omitted)
		}
	}
	for _, required := range []string{"CREATE TABLE identities", "CREATE TABLE password_credentials", "CREATE TABLE oauth_clients"} {
		if !strings.Contains(converted.String(), required) {
			t.Fatalf("MySQL schema is missing %q", required)
		}
	}
	for _, required := range []string{
		"primary_email VARCHAR(128)", "`key` VARCHAR(128)",
		"display_name LONGTEXT",
	} {
		if !strings.Contains(converted.String(), required) {
			t.Fatalf(
				"MySQL schema is missing indexed column conversion %q",
				required,
			)
		}
	}
	for column := range mysqlIndexedTextColumns {
		for _, invalidType := range []string{" TEXT", " LONGTEXT"} {
			if strings.Contains(converted.String(), " "+column+invalidType) ||
				strings.Contains(converted.String(), "\n"+column+invalidType) ||
				strings.Contains(converted.String(), "\t"+column+invalidType) {
				t.Fatalf(
					"MySQL indexed column %q uses an unindexable type",
					column,
				)
			}
		}
	}
}

func TestPostgreSQLSchemaDialectConversion(t *testing.T) {
	statements := schemaStatements(BackendPostgreSQL)
	converted := strings.Join(statements, "\n")
	for _, omitted := range []string{"schema_version", "identity_emails"} {
		if strings.Contains(converted, omitted) {
			t.Fatalf(
				"PostgreSQL schema contains legacy schema item %q",
				omitted,
			)
		}
	}
	for _, statement := range statements {
		for _, required := range []string{"public BOOLEAN", "trusted BOOLEAN", "request_json JSONB"} {
			if strings.Contains(statement, strings.Fields(required)[0]+" ") &&
				!strings.Contains(statement, required) {
				t.Fatalf(
					"PostgreSQL schema did not convert %q: %s",
					required,
					statement,
				)
			}
		}
		if strings.Contains(statement, "BLOB") ||
			strings.Contains(statement, "AUTOINCREMENT") {
			t.Fatalf("PostgreSQL schema contains SQLite syntax: %s", statement)
		}
	}
	for _, required := range []string{"CREATE TABLE identities", "CREATE TABLE password_credentials", "CREATE TABLE oauth_clients"} {
		if !strings.Contains(converted, required) {
			t.Fatalf("PostgreSQL schema is missing %q", required)
		}
	}
}

func TestPostgreSQLDriverErrorsRemainRedactedAndRetryable(t *testing.T) {
	driverError := &pgconn.PgError{
		Code:    "40001",
		Message: "password=do-not-log",
	}
	wrapped := databaseError("storage write failed", driverError)
	if wrapped.Error() != "storage write failed" ||
		strings.Contains(wrapped.Error(), "do-not-log") {
		t.Fatalf("database error leaked driver diagnostics: %q", wrapped)
	}
	if !isRetryableTransactionError(wrapped) {
		t.Fatal("serialization failure was not classified as retryable")
	}
	if isRetryableTransactionError(
		databaseError("storage write failed", &pgconn.PgError{Code: "23505"}),
	) {
		t.Fatal("unique violation was classified as transaction-retryable")
	}
	if !isConstraintError(&pgconn.PgError{Code: "23505"}) {
		t.Fatal(
			"PostgreSQL unique violation was not classified as a stable conflict",
		)
	}
}

func TestConfigFromEnvReadsDatasourceURLFileWithoutMixingSources(t *testing.T) {
	file := filepath.Join(t.TempDir(), "dsn")
	if err := os.WriteFile(
		file,
		[]byte("postgres://user:secret@db.example/test?sslmode=require\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBELOOP_DATASOURCE_URL_FILE", file)
	config, err := ConfigFromEnv()
	if err != nil || config.Backend != BackendPostgreSQL ||
		config.DatasourceURL == "" {
		t.Fatalf("ConfigFromEnv = %#v, %v", config, err)
	}
	t.Setenv(
		"KUBELOOP_DATASOURCE_URL",
		"postgres://other:secret@db.example/test?sslmode=require",
	)
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("ConfigFromEnv accepted both DSN sources")
	}
}

func TestConfigFromEnvParsesDatasourcePoolTimeoutAndRetrySettings(
	t *testing.T,
) {
	t.Setenv(
		"KUBELOOP_DATASOURCE_URL",
		"postgres://user:secret@db.example/test?sslmode=require",
	)
	t.Setenv("KUBELOOP_DATASOURCE_CONNECT_TIMEOUT", "7s")
	t.Setenv("KUBELOOP_DATASOURCE_QUERY_TIMEOUT", "3s")
	t.Setenv("KUBELOOP_DATASOURCE_MAX_OPEN_CONNECTIONS", "12")
	t.Setenv("KUBELOOP_DATASOURCE_MAX_IDLE_CONNECTIONS", "4")
	t.Setenv("KUBELOOP_DATASOURCE_CONNECTION_MAX_LIFETIME", "20m")
	t.Setenv("KUBELOOP_DATASOURCE_TRANSACTION_MAX_RETRIES", "5")
	t.Setenv("KUBELOOP_DATASOURCE_TRANSACTION_RETRY_BACKOFF", "40ms")
	config, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.ConnectTimeout != 7*time.Second || config.QueryTimeout != 3*time.Second ||
		config.MaxOpenConnections != 12 ||
		config.MaxIdleConnections != 4 ||
		config.ConnectionMaxLifetime != 20*time.Minute ||
		config.TransactionMaxRetries != 5 ||
		config.TransactionRetryBackoff != 40*time.Millisecond {
		t.Fatalf("PostgreSQL tuning config = %#v", config)
	}
	t.Setenv("KUBELOOP_DATASOURCE_MAX_OPEN_CONNECTIONS", "invalid")
	if _, err := ConfigFromEnv(); err == nil ||
		!strings.Contains(err.Error(), "decode storage environment") {
		t.Fatalf("invalid PostgreSQL pool error = %v", err)
	}
}

func TestConfigFromEnvParsesDatasourceSecurityBoolean(t *testing.T) {
	t.Setenv(
		"KUBELOOP_DATASOURCE_URL",
		"mysql://user:secret@db.example/test?tls=false",
	)
	t.Setenv("KUBELOOP_DATASOURCE_ALLOW_INSECURE", "true")
	config, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.Backend != BackendMySQL || !config.AllowInsecureDatasource ||
		config.ControlPlaneReplicas != 1 {
		t.Fatalf("environment config = %#v", config)
	}
}

func openSQLiteTestStore(t *testing.T, path string) *Store {
	t.Helper()
	store, err := Open(
		context.Background(),
		Config{Backend: BackendSQLite, SQLitePath: path},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func assertSQLitePragma(t *testing.T, store *Store, name, expected string) {
	t.Helper()
	var value string
	if err := store.db.QueryRow("PRAGMA " + name).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != expected {
		t.Fatalf("PRAGMA %s = %q, want %q", name, value, expected)
	}
}

func TestIdentityNotFoundUsesStableError(t *testing.T) {
	store := openSQLiteTestStore(t, filepath.Join(t.TempDir(), "kubeloop.db"))
	_, err := store.Identities().GetByID(context.Background(), uuid.NewString())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID error = %v", err)
	}
}
