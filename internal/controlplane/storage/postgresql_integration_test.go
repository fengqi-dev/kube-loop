package storage

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// These tests intentionally require a real PostgreSQL server. CI and local
// verification opt in with a URL in KUBELOOP_TEST_POSTGRESQL_DSN; every test
// creates and drops its own schema, so it never mutates the configured public
// schema or depends on a pre-existing KubeLoop database.
func TestPostgreSQLBackendIntegration(t *testing.T) {
	config, cleanup := newPostgreSQLIntegrationConfig(t)
	defer cleanup()
	config.MaxOpenConnections = 7
	config.MaxIdleConnections = 3
	config.QueryTimeout = 100 * time.Millisecond

	store, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if store.Backend() != BackendPostgreSQL {
		t.Fatalf("backend = %q", store.Backend())
	}
	if version, err := store.SchemaVersion(context.Background()); err != nil || version != currentSchemaVersion() {
		t.Fatalf("schema version = %d, error = %v", version, err)
	}
	if stats := store.db.Stats(); stats.MaxOpenConnections != 7 {
		t.Fatalf("max open connections = %d", stats.MaxOpenConnections)
	}
	if err := store.Check(context.Background()); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	if _, err := store.db.ExecContext(context.Background(), `SELECT pg_sleep(0.5)`); err == nil {
		t.Fatal("PostgreSQL statement timeout did not cancel a slow query")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("statement timeout took %s", elapsed)
	}
	if err := store.Check(context.Background()); err != nil {
		t.Fatalf("storage did not recover after statement timeout: %v", err)
	}

	ctx := context.Background()
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	_, err = store.Principals().Upsert(ctx, Principal{
		ID: uuid.NewString(), Provider: "oidc", ExternalID: "postgres-user", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	rollbackID := uuid.NewString()
	sentinel := errors.New("rollback")
	err = store.WithinTransaction(ctx, func(repositories Repositories) error {
		_, err := repositories.Principals().Upsert(ctx, Principal{
			ID: rollbackID, Provider: "oidc", ExternalID: "rolled-back", CreatedAt: now,
		})
		if err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("rollback error = %v", err)
	}
	if _, err := store.Principals().GetByID(ctx, rollbackID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rolled-back principal lookup = %v", err)
	}
}

func TestPostgreSQLMigrationFailureRollsBackVersion(t *testing.T) {
	config, cleanup := newPostgreSQLIntegrationConfig(t)
	defer cleanup()
	database, err := sql.Open("pgx", config.DatasourceURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE principals (broken TEXT)`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), config); err == nil || !strings.Contains(err.Error(), "migration 20") {
		t.Fatalf("PostgreSQL migration error = %v", err)
	}
	database, err = sql.Open("pgx", config.DatasourceURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var migrationTable sql.NullString
	if err := database.QueryRow(`SELECT to_regclass('schema_migrations')`).Scan(&migrationTable); err != nil {
		t.Fatal(err)
	}
	if migrationTable.Valid {
		t.Fatalf("failed PostgreSQL migration left table %q", migrationTable.String)
	}
}

func TestPostgreSQLRejectsNewerSchema(t *testing.T) {
	config, cleanup := newPostgreSQLIntegrationConfig(t)
	defer cleanup()
	store, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES ($1, $2)`, 999, "now"); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), config); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("Open newer PostgreSQL schema error = %v", err)
	}
}

func TestPostgreSQLConcurrentMigrationsAndSerializableRetryIntegration(t *testing.T) {
	config, cleanup := newPostgreSQLIntegrationConfig(t)
	defer cleanup()
	config.QueryTimeout = 5 * time.Second
	config.TransactionMaxRetries = 3
	config.TransactionRetryBackoff = time.Millisecond

	const workers = 6
	stores := make([]*Store, workers)
	errorsCh := make(chan error, workers)
	var group sync.WaitGroup
	for index := range workers {
		group.Go(func() {
			store, err := Open(context.Background(), config)
			if err != nil {
				errorsCh <- err
				return
			}
			stores[index] = store
		})
	}
	group.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Error(err)
	}
	for _, store := range stores {
		if store == nil {
			t.Fatal("concurrent migration did not return every Store")
		}
		defer store.Close()
		if version, err := store.SchemaVersion(context.Background()); err != nil || version != currentSchemaVersion() {
			t.Fatalf("concurrent schema version = %d, error = %v", version, err)
		}
	}

	store := stores[0]
	if _, err := store.db.Exec(`CREATE TABLE retry_counter (id INTEGER PRIMARY KEY, value INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO retry_counter(id, value) VALUES (1, 0)`); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	releaseFirstAttempts := make(chan struct{})
	var firstReads atomic.Int32
	var callbackCalls atomic.Int32
	transactionErrors := make(chan error, 2)
	for range 2 {
		go func() {
			transactionErrors <- store.WithinTransaction(ctx, func(repositories Repositories) error {
				callbackCalls.Add(1)
				set, ok := repositories.(*repositorySet)
				if !ok {
					return errors.New("unexpected repository implementation")
				}
				var isolation string
				if err := set.principals.executor.QueryRowContext(ctx, `SHOW transaction_isolation`).Scan(&isolation); err != nil {
					return databaseError("read transaction isolation", err)
				}
				if isolation != "serializable" {
					return errors.New("PostgreSQL transaction is not serializable")
				}
				var value int
				if err := set.principals.executor.QueryRowContext(ctx, `SELECT value FROM retry_counter WHERE id = 1`).Scan(&value); err != nil {
					return databaseError("read retry counter", err)
				}
				readNumber := firstReads.Add(1)
				if readNumber <= 2 {
					if readNumber == 2 {
						close(releaseFirstAttempts)
					}
					select {
					case <-releaseFirstAttempts:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				if _, err := set.principals.executor.ExecContext(ctx, `UPDATE retry_counter SET value = $1 WHERE id = 1`, value+1); err != nil {
					return databaseError("update retry counter", err)
				}
				return nil
			})
		}()
	}
	for range 2 {
		if err := <-transactionErrors; err != nil {
			t.Fatalf("serializable transaction failed after retries: %v", err)
		}
	}
	var value int
	if err := store.db.QueryRow(`SELECT value FROM retry_counter WHERE id = 1`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != 2 || callbackCalls.Load() < 3 {
		t.Fatalf("retry counter = %d, callback calls = %d", value, callbackCalls.Load())
	}
}

func newPostgreSQLIntegrationConfig(t *testing.T) (Config, func()) {
	t.Helper()
	rawDSN := strings.TrimSpace(os.Getenv("KUBELOOP_TEST_POSTGRESQL_DSN"))
	if rawDSN == "" {
		t.Skip("KUBELOOP_TEST_POSTGRESQL_DSN is not configured")
	}
	adminConfig, err := pgx.ParseConfig(rawDSN)
	if err != nil {
		t.Fatal("invalid KUBELOOP_TEST_POSTGRESQL_DSN")
	}
	admin := stdlib.OpenDB(*adminConfig)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		t.Fatal("connect to PostgreSQL integration database")
	}
	schema := "kubeloop_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		_ = admin.Close()
		t.Fatal("create PostgreSQL integration schema")
	}
	testConfig := adminConfig.Copy()
	testURL, err := url.Parse(rawDSN)
	if err != nil || (testURL.Scheme != "postgres" && testURL.Scheme != "postgresql") {
		_, _ = admin.ExecContext(ctx, `DROP SCHEMA "`+schema+`" CASCADE`)
		_ = admin.Close()
		t.Fatal("KUBELOOP_TEST_POSTGRESQL_DSN must be a postgres:// or postgresql:// URL")
	}
	query := testURL.Query()
	query.Set("search_path", schema)
	testURL.RawQuery = query.Encode()
	cleanup := func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupContext, `DROP SCHEMA "`+schema+`" CASCADE`)
		_ = admin.Close()
	}
	return Config{
		Backend: BackendPostgreSQL, DatasourceURL: testURL.String(),
		ConnectTimeout: 10 * time.Second, QueryTimeout: 5 * time.Second,
		MaxOpenConnections: 10, MaxIdleConnections: 5,
		AllowInsecureDatasource: testConfig.TLSConfig == nil,
	}, cleanup
}
