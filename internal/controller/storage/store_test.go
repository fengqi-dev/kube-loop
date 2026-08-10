package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestSQLiteOpenMigratesAndConfiguresDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "kubeloop.db")
	store, err := Open(context.Background(), Config{Backend: BackendSQLite, SQLitePath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if store.Backend() != BackendSQLite {
		t.Fatalf("backend = %q", store.Backend())
	}
	version, err := store.SchemaVersion(context.Background())
	if err != nil || version != currentSchemaVersion() {
		t.Fatalf("schema version = %d, error = %v", version, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
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

func TestSQLiteTaskStateMigrationPreservesLegacyTasks(t *testing.T) {
	testTaskStateMigrationPreservesLegacyTasks(t, Config{
		Backend: BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "legacy-tasks.db"),
	})
}

func testTaskStateMigrationPreservesLegacyTasks(t *testing.T, config Config) {
	t.Helper()
	store, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	principal, err := store.Principals().Upsert(ctx, Principal{
		ID: uuid.NewString(), Provider: "oidc", ExternalID: "legacy-owner", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	network, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.2.0.0/16"}, ServiceCIDRs: []string{"10.96.0.0/12"},
		ServiceIPs: []string{"10.96.0.10"}, DNSServer: "10.96.0.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	networkJSON, _ := networkspec.CanonicalJSON(network)
	networkHash, _ := networkspec.Hash(network)
	session := Session{
		ID: uuid.NewString(), PrincipalID: principal.ID, DeviceID: "legacy-client", ClusterID: "cluster-a",
		Namespace: "development", State: "active", Generation: 1, CreatedAt: now, UpdatedAt: now,
		LastHeartbeatAt: now, ExpiresAt: now.Add(time.Hour), NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
	}
	if err := store.Sessions().Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	expiresAt := session.ExpiresAt
	activeID, preparingID := uuid.NewString(), uuid.NewString()
	for _, task := range []Task{
		{ID: activeID, PrincipalID: principal.ID, SessionID: session.ID, Type: "port-forward", State: remotetask.Running, Spec: json.RawMessage(`{}`), IdempotencyKey: "legacy-active", CreatedAt: now, UpdatedAt: now, ExpiresAt: &expiresAt},
		{ID: preparingID, PrincipalID: principal.ID, SessionID: session.ID, Type: "exchange", State: remotetask.Starting, Spec: json.RawMessage(`{}`), IdempotencyKey: "legacy-preparing", CreatedAt: now, UpdatedAt: now, ExpiresAt: &expiresAt},
	} {
		if err := store.Tasks().Create(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.ExecContext(ctx, store.repositories.principals.bind(`UPDATE tasks SET state = 'active' WHERE id = ?`), activeID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, store.repositories.principals.bind(`UPDATE tasks SET state = 'preparing' WHERE id = ?`), preparingID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DROP TABLE management_metadata`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version >= 6`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	active, err := store.Tasks().GetByID(ctx, activeID)
	if err != nil || active.State != remotetask.Running {
		t.Fatalf("migrated active Task = %#v, %v", active, err)
	}
	preparing, err := store.Tasks().GetByID(ctx, preparingID)
	if err != nil || preparing.State != remotetask.Starting {
		t.Fatalf("migrated preparing Task = %#v, %v", preparing, err)
	}
}

func TestSQLitePrincipalRepositoryPersistsIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeloop.db")
	store := openSQLiteTestStore(t, path)
	created := time.Date(2026, 8, 9, 1, 2, 3, 0, time.UTC)
	first, err := store.Principals().Upsert(context.Background(), Principal{
		ID: uuid.NewString(), Provider: "oidc-company", ExternalID: "subject-1",
		DisplayName: "First Name", Email: "first@example.test", Groups: []string{"developers"}, CreatedAt: created,
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.Principals().Upsert(context.Background(), Principal{
		ID: uuid.NewString(), Provider: "oidc-company", ExternalID: "subject-1",
		DisplayName: "Updated Name", Email: "updated@example.test", Groups: []string{"developers", "admins"},
		CreatedAt: created.Add(time.Hour), UpdatedAt: created.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != first.ID || !updated.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("identity upsert replaced stable fields: first=%#v updated=%#v", first, updated)
	}
	if updated.DisplayName != "Updated Name" || len(updated.Groups) != 2 {
		t.Fatalf("identity upsert did not update profile: %#v", updated)
	}
	byID, err := store.Principals().GetByID(context.Background(), first.ID)
	if err != nil || byID.Email != "updated@example.test" {
		t.Fatalf("GetByID = %#v, %v", byID, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openSQLiteTestStore(t, path)
	byIdentity, err := store.Principals().GetByIdentity(context.Background(), "oidc-company", "subject-1")
	if err != nil || byIdentity.ID != first.ID {
		t.Fatalf("persisted identity = %#v, %v", byIdentity, err)
	}
}

func TestSQLiteConcurrentIdentityUpsertHasOneStableID(t *testing.T) {
	store := openSQLiteTestStore(t, filepath.Join(t.TempDir(), "kubeloop.db"))
	const workers = 16
	results := make(chan string, workers)
	errorsCh := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Go(func() {
			principal, err := store.Principals().Upsert(context.Background(), Principal{
				ID: uuid.NewString(), Provider: "oidc", ExternalID: "same-subject",
			})
			if err != nil {
				errorsCh <- err
				return
			}
			results <- principal.ID
		})
	}
	wait.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		t.Error(err)
	}
	stableID := ""
	for id := range results {
		if stableID == "" {
			stableID = id
		} else if id != stableID {
			t.Errorf("identity returned multiple IDs: %s and %s", stableID, id)
		}
	}
}

func TestSQLiteRejectsNewerSchemaAndUnsafePaths(t *testing.T) {
	t.Run("newer schema", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "kubeloop.db")
		store := openSQLiteTestStore(t, path)
		if _, err := store.db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (999, 'now')`); err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(context.Background(), Config{Backend: BackendSQLite, SQLitePath: path}); err == nil || !strings.Contains(err.Error(), "newer") {
			t.Fatalf("Open newer schema error = %v", err)
		}
	})
	t.Run("symbolic link", func(t *testing.T) {
		if runtime.GOOS == "windows" {
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
		if _, err := Open(context.Background(), Config{Backend: BackendSQLite, SQLitePath: link}); err == nil || !strings.Contains(err.Error(), "symbolic link") {
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

func TestSQLiteMigrationFailureRollsBackVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeloop.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE principals (broken TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), Config{Backend: BackendSQLite, SQLitePath: path}); err == nil || !strings.Contains(err.Error(), "migration 1") {
		t.Fatalf("Open migration error = %v", err)
	}
	database, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var migrationTableCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&migrationTableCount); err != nil {
		t.Fatal(err)
	}
	if migrationTableCount == 0 {
		return
	}
	var version int
	if err := database.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 0 {
		t.Fatalf("failed migration recorded schema version %d", version)
	}
}

func TestSQLiteRejectsReadOnlyDatabase(t *testing.T) {
	if runtime.GOOS == "windows" {
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
	reopened, err := Open(context.Background(), Config{Backend: BackendSQLite, SQLitePath: path})
	if err == nil {
		_ = reopened.Close()
		t.Skip("current user can bypass read-only mode (usually a privileged test runner)")
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
		t.Fatal("audit write unexpectedly succeeded with max_page_count exhausted")
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
	principalID := uuid.NewString()
	command := exec.Command(os.Args[0], "-test.run=^TestSQLiteCrashHelper$")
	command.Env = append(os.Environ(),
		"KUBELOOP_SQLITE_CRASH_HELPER=1",
		"KUBELOOP_SQLITE_CRASH_PATH="+path,
		"KUBELOOP_SQLITE_CRASH_PRINCIPAL="+principalID,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("crash helper: %v: %s", err, output)
	}
	store = openSQLiteTestStore(t, path)
	if _, err := store.Principals().GetByID(context.Background(), principalID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("uncommitted principal survived process exit: %v", err)
	}
	if err := store.checkSQLiteIntegrity(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteCrashHelper(t *testing.T) {
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
	_, err = repositories.Principals().Upsert(context.Background(), Principal{
		ID: os.Getenv("KUBELOOP_SQLITE_CRASH_PRINCIPAL"), Provider: "oidc", ExternalID: "crash-helper",
	})
	if err != nil {
		os.Exit(4)
	}
	// Exit without Commit, Rollback or Close to simulate abrupt process loss.
	os.Exit(0)
}

func TestStorageConfigurationSecurity(t *testing.T) {
	if _, err := (Config{Backend: BackendSQLite, SQLitePath: "db", ControllerReplicas: 2}).normalized(); err == nil {
		t.Fatal("SQLite accepted multiple Controller replicas")
	}
	if _, err := (Config{Backend: BackendPostgreSQL, PostgreSQLDSN: "postgres://user:secret@db.example/test?sslmode=disable"}).normalized(); err == nil {
		t.Fatal("PostgreSQL accepted disabled TLS")
	}
	if _, err := (Config{Backend: BackendPostgreSQL, PostgreSQLDSN: "postgres://user:secret@db.example/test?sslmode=prefer"}).normalized(); err == nil {
		t.Fatal("PostgreSQL accepted downgrade-capable preferred TLS")
	}
	if _, err := (Config{Backend: BackendPostgreSQL, PostgreSQLDSN: "postgres://user:secret@db.example/test?sslmode=require"}).normalized(); err != nil {
		t.Fatalf("PostgreSQL TLS config rejected: %v", err)
	}
	redacted := RedactedPostgreSQLDSN("postgres://user:secret@db.example/test?sslmode=require")
	if strings.Contains(redacted, "secret") || !strings.Contains(redacted, "REDACTED") {
		t.Fatalf("redacted DSN = %q", redacted)
	}
	redacted = RedactedPostgreSQLDSN("host=db.example user=app password=secret sslmode=require")
	if strings.Contains(redacted, "secret") || !strings.Contains(redacted, "REDACTED") {
		t.Fatalf("keyword DSN was not redacted: %q", redacted)
	}
	redacted = RedactedPostgreSQLDSN("host=db.example user=app password='secret with spaces' sslmode=require")
	if strings.Contains(redacted, "secret with spaces") || !strings.Contains(redacted, "REDACTED") {
		t.Fatalf("quoted keyword DSN was not fully redacted: %q", redacted)
	}
	redacted = RedactedPostgreSQLDSN("postgres://user@db.example/test?sslmode=require&password=query-secret")
	if strings.Contains(redacted, "query-secret") || !strings.Contains(redacted, "REDACTED") {
		t.Fatalf("URL query password was not redacted: %q", redacted)
	}
}

func TestPostgreSQLDriverErrorsRemainRedactedAndRetryable(t *testing.T) {
	driverError := &pgconn.PgError{Code: "40001", Message: "password=do-not-log"}
	wrapped := databaseError("storage write failed", driverError)
	if wrapped.Error() != "storage write failed" || strings.Contains(wrapped.Error(), "do-not-log") {
		t.Fatalf("database error leaked driver diagnostics: %q", wrapped)
	}
	if !isRetryableTransactionError(wrapped) {
		t.Fatal("serialization failure was not classified as retryable")
	}
	if isRetryableTransactionError(databaseError("storage write failed", &pgconn.PgError{Code: "23505"})) {
		t.Fatal("unique violation was classified as transaction-retryable")
	}
	if !isConstraintError(&pgconn.PgError{Code: "23505"}) {
		t.Fatal("PostgreSQL unique violation was not classified as a stable conflict")
	}
}

func TestConfigFromEnvReadsDSNFileWithoutMixingSources(t *testing.T) {
	file := filepath.Join(t.TempDir(), "dsn")
	if err := os.WriteFile(file, []byte("postgres://user:secret@db.example/test?sslmode=require\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBELOOP_STORAGE_TYPE", "postgresql")
	t.Setenv("KUBELOOP_POSTGRESQL_DSN_FILE", file)
	config, err := ConfigFromEnv()
	if err != nil || config.Backend != BackendPostgreSQL || config.PostgreSQLDSN == "" {
		t.Fatalf("ConfigFromEnv = %#v, %v", config, err)
	}
	t.Setenv("KUBELOOP_POSTGRESQL_DSN", "postgres://other:secret@db.example/test?sslmode=require")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("ConfigFromEnv accepted both DSN sources")
	}
}

func TestConfigFromEnvParsesPostgreSQLPoolTimeoutAndRetrySettings(t *testing.T) {
	t.Setenv("KUBELOOP_STORAGE_TYPE", "postgresql")
	t.Setenv("KUBELOOP_POSTGRESQL_DSN", "postgres://user:secret@db.example/test?sslmode=require")
	t.Setenv("KUBELOOP_POSTGRESQL_CONNECT_TIMEOUT", "7s")
	t.Setenv("KUBELOOP_POSTGRESQL_QUERY_TIMEOUT", "3s")
	t.Setenv("KUBELOOP_POSTGRESQL_MAX_OPEN_CONNECTIONS", "12")
	t.Setenv("KUBELOOP_POSTGRESQL_MAX_IDLE_CONNECTIONS", "4")
	t.Setenv("KUBELOOP_POSTGRESQL_CONNECTION_MAX_LIFETIME", "20m")
	t.Setenv("KUBELOOP_POSTGRESQL_TRANSACTION_MAX_RETRIES", "5")
	t.Setenv("KUBELOOP_POSTGRESQL_TRANSACTION_RETRY_BACKOFF", "40ms")
	config, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.ConnectTimeout != 7*time.Second || config.QueryTimeout != 3*time.Second ||
		config.MaxOpenConnections != 12 || config.MaxIdleConnections != 4 ||
		config.ConnectionMaxLifetime != 20*time.Minute || config.TransactionMaxRetries != 5 ||
		config.TransactionRetryBackoff != 40*time.Millisecond {
		t.Fatalf("PostgreSQL tuning config = %#v", config)
	}
	t.Setenv("KUBELOOP_POSTGRESQL_MAX_OPEN_CONNECTIONS", "invalid")
	if _, err := ConfigFromEnv(); err == nil || !strings.Contains(err.Error(), "KUBELOOP_POSTGRESQL_MAX_OPEN_CONNECTIONS") {
		t.Fatalf("invalid PostgreSQL pool error = %v", err)
	}
}

func openSQLiteTestStore(t *testing.T, path string) *Store {
	t.Helper()
	store, err := Open(context.Background(), Config{Backend: BackendSQLite, SQLitePath: path})
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

func TestPrincipalNotFoundUsesStableError(t *testing.T) {
	store := openSQLiteTestStore(t, filepath.Join(t.TempDir(), "kubeloop.db"))
	_, err := store.Principals().GetByID(context.Background(), uuid.NewString())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID error = %v", err)
	}
}
