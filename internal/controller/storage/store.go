package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite"
)

type Store struct {
	db                      *sql.DB
	orm                     *bun.DB
	backend                 Backend
	queryTimeout            time.Duration
	transactionMaxRetries   int
	transactionRetryBackoff time.Duration
	repositories            *repositorySet
}

var _ Repositories = (*Store)(nil)
var _ TransactionManager = (*Store)(nil)

func Open(ctx context.Context, rawConfig Config) (*Store, error) {
	config, err := rawConfig.normalized()
	if err != nil {
		return nil, err
	}
	var database *sql.DB
	if config.Backend == BackendSQLite {
		dsn, prepareErr := prepareSQLite(config)
		if prepareErr != nil {
			return nil, prepareErr
		}
		database, err = sql.Open("sqlite", dsn)
	} else {
		postgresConfig, parseErr := pgx.ParseConfig(config.PostgreSQLDSN)
		if parseErr != nil {
			return nil, errors.New("initialize PostgreSQL storage configuration")
		}
		postgresConfig.ConnectTimeout = config.ConnectTimeout
		if postgresConfig.RuntimeParams == nil {
			postgresConfig.RuntimeParams = make(map[string]string)
		}
		queryTimeoutMilliseconds := (config.QueryTimeout + time.Millisecond - 1) / time.Millisecond
		postgresConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(int64(queryTimeoutMilliseconds), 10)
		database = stdlib.OpenDB(*postgresConfig)
	}
	if err != nil {
		return nil, fmt.Errorf("initialize %s storage driver", config.Backend)
	}
	database.SetMaxOpenConns(config.MaxOpenConnections)
	database.SetMaxIdleConns(config.MaxIdleConnections)
	database.SetConnMaxLifetime(config.ConnectionMaxLifetime)
	connectContext, cancel := context.WithTimeout(ctx, config.ConnectTimeout)
	defer cancel()
	if err := database.PingContext(connectContext); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("connect to %s storage: %w", config.Backend, err)
	}
	orm := bun.NewDB(database, pgdialect.New())
	if config.Backend == BackendSQLite {
		orm = bun.NewDB(database, sqlitedialect.New())
	}
	store := &Store{
		db: database, orm: orm, backend: config.Backend, queryTimeout: config.QueryTimeout,
		transactionMaxRetries:   config.TransactionMaxRetries,
		transactionRetryBackoff: config.TransactionRetryBackoff,
	}
	store.repositories = newRepositorySet(config.Backend, database, orm)
	store.repositories.setTaskTransactionManager(store)
	if err := store.migrate(connectContext); err != nil {
		_ = database.Close()
		return nil, err
	}
	if config.Backend == BackendSQLite {
		if err := store.checkSQLiteIntegrity(connectContext); err != nil {
			_ = database.Close()
			return nil, err
		}
		if err := os.Chmod(config.SQLitePath, 0o600); err != nil {
			_ = database.Close()
			return nil, fmt.Errorf("set SQLite database permissions: %w", err)
		}
	}
	return store, nil
}

func prepareSQLite(config Config) (string, error) {
	absolute, err := filepath.Abs(config.SQLitePath)
	if err != nil {
		return "", fmt.Errorf("resolve SQLite path: %w", err)
	}
	config.SQLitePath = absolute
	directory := filepath.Dir(absolute)
	if info, err := os.Lstat(directory); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("SQLite directory must not be a symbolic link")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create SQLite directory: %w", err)
	}
	if info, err := os.Lstat(absolute); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("SQLite database must not be a symbolic link")
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("SQLite database path must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect SQLite database: %w", err)
	}
	file, err := os.OpenFile(absolute, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", fmt.Errorf("create SQLite database: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close SQLite database: %w", err)
	}
	dsn := sqliteFileURL(absolute, runtime.GOOS == "windows")
	query := url.Values{}
	query.Add("_pragma", "busy_timeout("+strconv.FormatInt(config.BusyTimeout.Milliseconds(), 10)+")")
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(NORMAL)")
	query.Add("_txlock", "immediate")
	return dsn + "?" + query.Encode(), nil
}

func sqliteFileURL(absolute string, windows bool) string {
	path := filepath.ToSlash(absolute)
	if windows {
		// filepath.ToSlash follows the host OS. Tests exercise this conversion on
		// non-Windows builders too, so normalize Windows separators explicitly.
		path = strings.ReplaceAll(absolute, `\`, "/")
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func (store *Store) migrate(ctx context.Context) error {
	transaction, err := store.orm.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin storage migration")
	}
	defer transaction.Rollback()
	if store.backend == BackendPostgreSQL {
		if _, err := transaction.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1263816527)`); err != nil {
			return errors.New("acquire PostgreSQL migration lock")
		}
	}
	if _, err := transaction.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return errors.New("initialize storage migration table")
	}
	var version int
	if err := transaction.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return errors.New("read storage schema version")
	}
	if version > currentSchemaVersion() {
		return fmt.Errorf("storage schema version %d is newer than supported version %d", version, currentSchemaVersion())
	}
	for _, migration := range migrations {
		if migration.version <= version {
			continue
		}
		statements := migration.sqlite
		if store.backend == BackendPostgreSQL {
			statements = migration.postgresql
		}
		for statementIndex, statement := range statements {
			if _, err := transaction.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply storage migration %d statement %d", migration.version, statementIndex+1)
			}
		}
		insert := `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`
		if _, err := transaction.ExecContext(ctx, insert, migration.version, formatTime(time.Now())); err != nil {
			return fmt.Errorf("record storage migration %d", migration.version)
		}
	}
	if err := transaction.Commit(); err != nil {
		return errors.New("commit storage migration")
	}
	return nil
}

func (store *Store) checkSQLiteIntegrity(ctx context.Context) error {
	var result string
	if err := store.db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&result); err != nil {
		return errors.New("run SQLite integrity check")
	}
	if !strings.EqualFold(result, "ok") {
		return errors.New("SQLite integrity check failed")
	}
	return nil
}

func (store *Store) Check(ctx context.Context) error {
	queryContext, cancel := context.WithTimeout(ctx, store.queryTimeout)
	defer cancel()
	if err := store.db.PingContext(queryContext); err != nil {
		return errors.New("storage unavailable")
	}
	return nil
}

func (store *Store) Close() error {
	return store.db.Close()
}

func (store *Store) Backend() Backend {
	return store.backend
}

func (store *Store) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	if err := store.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return 0, errors.New("read storage schema version")
	}
	return version, nil
}

func (store *Store) Principals() PrincipalRepository {
	return store.repositories.Principals()
}

func (store *Store) TokenFamilies() TokenFamilyRepository {
	return store.repositories.TokenFamilies()
}

func (store *Store) RefreshTokens() RefreshTokenRepository {
	return store.repositories.RefreshTokens()
}

func (store *Store) Sessions() SessionRepository {
	return store.repositories.Sessions()
}

func (store *Store) Tasks() TaskRepository {
	return store.repositories.Tasks()
}

func (store *Store) ResourceSnapshots() ResourceSnapshotRepository {
	return store.repositories.ResourceSnapshots()
}

func (store *Store) Idempotency() IdempotencyRepository {
	return store.repositories.Idempotency()
}

func (store *Store) Audit() AuditRepository {
	return store.repositories.Audit()
}

func (store *Store) RelayDesiredStates() RelayDesiredStateRepository {
	return store.repositories.RelayDesiredStates()
}

func (store *Store) AuditExportJobs() AuditExportJobRepository {
	return store.repositories.AuditExportJobs()
}

func (store *Store) AuthTransactions() AuthTransactionRepository {
	return store.repositories.AuthTransactions()
}

func (store *Store) ManagementState() ManagementStateRepository {
	return store.repositories.ManagementState()
}

func (store *Store) AdminSessions() AdminSessionRepository {
	return store.repositories.AdminSessions()
}

func (store *Store) AdminPolicyRevisions() AdminPolicyRevisionRepository {
	return store.repositories.AdminPolicyRevisions()
}

func (store *Store) ProviderConfigRevisions() ProviderConfigRevisionRepository {
	return store.repositories.ProviderConfigRevisions()
}

func (store *Store) AdminAssignments() AdminAssignmentRepository {
	return store.repositories.AdminAssignments()
}

func (store *Store) ActiveManagementRevisions() ActiveManagementRevisionRepository {
	return store.repositories.ActiveManagementRevisions()
}

func (store *Store) ConfigChangeRequests() ConfigChangeRequestRepository {
	return store.repositories.ConfigChangeRequests()
}

func (store *Store) WithinTransaction(ctx context.Context, function func(Repositories) error) error {
	if function == nil {
		return errors.New("transaction callback is required")
	}
	for attempt := 0; ; attempt++ {
		err := store.withinTransactionAttempt(ctx, function)
		if err == nil || store.backend != BackendPostgreSQL ||
			!isRetryableTransactionError(err) || attempt >= store.transactionMaxRetries {
			return err
		}
		if err := waitForTransactionRetry(ctx, store.transactionRetryBackoff, attempt); err != nil {
			return err
		}
	}
}

func (store *Store) withinTransactionAttempt(ctx context.Context, function func(Repositories) error) error {
	var options *sql.TxOptions
	if store.backend == BackendPostgreSQL {
		options = &sql.TxOptions{Isolation: sql.LevelSerializable}
	}
	transaction, err := store.orm.BeginTx(ctx, options)
	if err != nil {
		return databaseError("begin storage transaction", err)
	}
	defer transaction.Rollback()
	if err := function(newRepositorySet(store.backend, transaction.Tx, transaction)); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return databaseError("commit storage transaction", err)
	}
	return nil
}

func waitForTransactionRetry(ctx context.Context, base time.Duration, attempt int) error {
	delay := min(base*time.Duration(1<<attempt), time.Second)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func formatTime(value time.Time) string {
	// Fixed-width UTC timestamps preserve chronological order in both SQLite
	// TEXT indexes and PostgreSQL TEXT indexes, including sub-second values.
	return value.UTC().Format("2006-01-02T15:04:05.000000000Z")
}
