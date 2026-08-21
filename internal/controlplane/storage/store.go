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
	"github.com/uptrace/bun/dialect/mysqldialect"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite" // Register the pure-Go SQLite database/sql driver used by this backend.
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
var _ ExplicitTransactionManager = (*Store)(nil)

type repositoryTransaction struct {
	transaction  bun.Tx
	repositories Repositories
}

func (transaction *repositoryTransaction) Repositories() Repositories {
	return transaction.repositories
}

func (transaction *repositoryTransaction) Commit() error { return transaction.transaction.Commit() }

func (transaction *repositoryTransaction) Rollback() error { return transaction.transaction.Rollback() }

func Open(ctx context.Context, rawConfig Config) (*Store, error) {
	config, err := rawConfig.normalized()
	if err != nil {
		return nil, err
	}
	var database *sql.DB
	switch config.Backend {
	case BackendSQLite:
		dsn, prepareErr := prepareSQLite(config)
		if prepareErr != nil {
			return nil, prepareErr
		}
		database, err = sql.Open("sqlite", dsn)
	case BackendPostgreSQL:
		postgresConfig, parseErr := pgx.ParseConfig(config.DatasourceURL)
		if parseErr != nil {
			return nil, errors.New(
				"initialize PostgreSQL storage configuration",
			)
		}
		postgresConfig.ConnectTimeout = config.ConnectTimeout
		if postgresConfig.RuntimeParams == nil {
			postgresConfig.RuntimeParams = make(map[string]string)
		}
		queryTimeoutMillis := int64(
			(config.QueryTimeout + time.Millisecond - 1) / time.Millisecond,
		)
		postgresConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(
			queryTimeoutMillis,
			10,
		)
		database = stdlib.OpenDB(*postgresConfig)
	case BackendMySQL:
		mysqlConfig, parseErr := parseMySQLURL(config.DatasourceURL)
		if parseErr != nil {
			return nil, errors.New("initialize MySQL storage configuration")
		}
		mysqlConfig.Timeout = config.ConnectTimeout
		mysqlConfig.ReadTimeout = config.QueryTimeout
		mysqlConfig.WriteTimeout = config.QueryTimeout
		mysqlConfig.RejectReadOnly = true
		database, err = sql.Open(string(BackendMySQL), mysqlConfig.FormatDSN())
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
	var orm *bun.DB
	switch config.Backend {
	case BackendSQLite:
		orm = bun.NewDB(database, sqlitedialect.New())
	case BackendPostgreSQL:
		orm = bun.NewDB(database, pgdialect.New())
	case BackendMySQL:
		orm = bun.NewDB(database, mysqldialect.New())
	}
	store := &Store{
		db: database, orm: orm, backend: config.Backend, queryTimeout: config.QueryTimeout,
		transactionMaxRetries:   config.TransactionMaxRetries,
		transactionRetryBackoff: config.TransactionRetryBackoff,
	}
	store.repositories = newRepositorySet(config.Backend, database, orm)
	store.repositories.setTaskTransactionManager(store)
	if err := store.initializeSchema(connectContext); err != nil {
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
	if info, err := os.Lstat(directory); err == nil &&
		info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("sQLite directory must not be a symbolic link")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create SQLite directory: %w", err)
	}
	if info, err := os.Lstat(absolute); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("sQLite database must not be a symbolic link")
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("sQLite database path must be a regular file")
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
	dsn := sqliteFileURL(absolute, runtime.GOOS == operatingSystemWindows)
	query := url.Values{}
	query.Add(
		"_pragma",
		"busy_timeout("+strconv.FormatInt(
			config.BusyTimeout.Milliseconds(),
			10,
		)+")",
	)
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

func (store *Store) initializeSchema(ctx context.Context) error {
	transaction, err := store.orm.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin storage initialization")
	}
	defer func() { _ = transaction.Rollback() }()
	mysqlInitializationLock := false
	defer func() {
		if mysqlInitializationLock {
			_, _ = transaction.ExecContext(
				context.WithoutCancel(ctx),
				`SELECT RELEASE_LOCK('kubeloop-storage-initialization')`,
			)
		}
	}()
	switch store.backend {
	case BackendSQLite:
	case BackendPostgreSQL:
		if _, err := transaction.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1263816527)`); err != nil {
			return errors.New("acquire PostgreSQL storage initialization lock")
		}
	case BackendMySQL:
		var acquired int
		lockQuery := `SELECT GET_LOCK('kubeloop-storage-initialization', 10)`
		if err := transaction.QueryRowContext(ctx, lockQuery).Scan(&acquired); err != nil ||
			acquired != 1 {
			return errors.New("acquire MySQL storage initialization lock")
		}
		mysqlInitializationLock = true
	}
	initialized, err := store.readSchemaID(ctx, transaction)
	if err != nil {
		return err
	}
	if initialized != "" {
		if initialized != currentSchemaID {
			return fmt.Errorf(
				"storage schema %q is unsupported; recreate the database",
				initialized,
			)
		}
		return commitStorageInitialization(
			ctx,
			transaction,
			&mysqlInitializationLock,
		)
	}
	empty, err := store.databaseIsEmpty(ctx, transaction)
	if err != nil {
		return err
	}
	if !empty {
		return errors.New(
			"storage database is not initialized with the current schema; recreate it",
		)
	}
	for statementIndex, statement := range schemaStatements(store.backend) {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf(
				"initialize storage schema statement %d: %w",
				statementIndex+1,
				err,
			)
		}
	}
	if _, err := transaction.ExecContext(ctx, `CREATE TABLE schema_metadata (
		id INTEGER PRIMARY KEY,
		schema_id TEXT NOT NULL,
		initialized_at TEXT NOT NULL
	)`); err != nil {
		return errors.New("create storage schema metadata")
	}
	insertMetadata := `INSERT INTO schema_metadata(id, schema_id, initialized_at) VALUES (?, ?, ?)`
	if _, err := transaction.ExecContext(
		ctx,
		insertMetadata,
		1,
		currentSchemaID,
		formatTime(time.Now()),
	); err != nil {
		return errors.New("record storage schema identity")
	}
	return commitStorageInitialization(
		ctx,
		transaction,
		&mysqlInitializationLock,
	)
}

func (store *Store) readSchemaID(
	ctx context.Context,
	transaction bun.Tx,
) (string, error) {
	var exists int
	var query string
	switch store.backend {
	case BackendSQLite:
		query = `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_metadata'`
	case BackendPostgreSQL:
		query = `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema()` +
			` AND table_name = 'schema_metadata'`
	case BackendMySQL:
		query = `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE()` +
			` AND table_name = 'schema_metadata'`
	}
	if err := transaction.QueryRowContext(ctx, query).Scan(&exists); err != nil {
		return "", errors.New("inspect storage schema metadata")
	}
	if exists == 0 {
		return "", nil
	}
	var schemaID string
	schemaIDQuery := `SELECT schema_id FROM schema_metadata WHERE id = 1`
	if err := transaction.QueryRowContext(ctx, schemaIDQuery).Scan(&schemaID); err != nil {
		return "", errors.New("read storage schema identity")
	}
	return schemaID, nil
}

func (store *Store) databaseIsEmpty(
	ctx context.Context,
	transaction bun.Tx,
) (bool, error) {
	var count int
	var query string
	switch store.backend {
	case BackendSQLite:
		query = `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`
	case BackendPostgreSQL:
		query = `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema()` +
			` AND table_type = 'BASE TABLE'`
	case BackendMySQL:
		query = `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE()` +
			` AND table_type = 'BASE TABLE'`
	}
	if err := transaction.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return false, errors.New("inspect storage database")
	}
	return count == 0, nil
}

func commitStorageInitialization(
	ctx context.Context,
	transaction bun.Tx,
	mysqlLock *bool,
) error {
	if *mysqlLock {
		if _, err := transaction.ExecContext(
			ctx,
			`SELECT RELEASE_LOCK('kubeloop-storage-initialization')`,
		); err != nil {
			return errors.New("release MySQL storage initialization lock")
		}
		*mysqlLock = false
	}
	if err := transaction.Commit(); err != nil {
		return errors.New("commit storage initialization")
	}
	return nil
}

func (store *Store) checkSQLiteIntegrity(ctx context.Context) error {
	var result string
	if err := store.db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&result); err != nil {
		return errors.New("run SQLite integrity check")
	}
	if !strings.EqualFold(result, "ok") {
		return errors.New("sQLite integrity check failed")
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

func (store *Store) Identities() IdentityRepository {
	return store.repositories.Identities()
}

func (store *Store) BootstrapTokens() BootstrapTokenRepository {
	return store.repositories.BootstrapTokens()
}

func (store *Store) Credentials() CredentialRepository { return store.repositories.Credentials() }

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

func (store *Store) AdminSessions() AdminSessionRepository {
	return store.repositories.AdminSessions()
}

func (store *Store) OAuthClients() OAuthClientRepository { return store.repositories.OAuthClients() }

func (store *Store) OAuthSessions() OAuthSessionRepository { return store.repositories.OAuthSessions() }

func (store *Store) OAuthConsents() OAuthConsentRepository { return store.repositories.OAuthConsents() }

func (store *Store) OAuthAuthorizationRequests() OAuthAuthorizationRequestRepository {
	return store.repositories.OAuthAuthorizationRequests()
}
func (store *Store) OAuthBrowserSessions() OAuthBrowserSessionRepository {
	return store.repositories.OAuthBrowserSessions()
}

func (store *Store) WithinTransaction(
	ctx context.Context,
	function func(Repositories) error,
) error {
	if function == nil {
		return errors.New("transaction callback is required")
	}
	for attempt := 0; ; attempt++ {
		err := store.withinTransactionAttempt(ctx, function)
		if err == nil || store.backend == BackendSQLite ||
			!isRetryableTransactionError(
				err,
			) || attempt >= store.transactionMaxRetries {
			return err
		}
		if err := waitForTransactionRetry(ctx, store.transactionRetryBackoff, attempt); err != nil {
			return err
		}
	}
}

func (store *Store) BeginTransaction(
	ctx context.Context,
) (RepositoryTransaction, error) {
	var options *sql.TxOptions
	if store.backend != BackendSQLite {
		options = &sql.TxOptions{Isolation: sql.LevelSerializable}
	}
	transaction, err := store.orm.BeginTx(ctx, options)
	if err != nil {
		return nil, databaseError("begin storage transaction", err)
	}
	return &repositoryTransaction{transaction: transaction,
		repositories: newRepositorySet(
			store.backend,
			transaction.Tx,
			transaction,
		)}, nil
}

func (store *Store) withinTransactionAttempt(
	ctx context.Context,
	function func(Repositories) error,
) error {
	var options *sql.TxOptions
	if store.backend != BackendSQLite {
		options = &sql.TxOptions{Isolation: sql.LevelSerializable}
	}
	transaction, err := store.orm.BeginTx(ctx, options)
	if err != nil {
		return databaseError("begin storage transaction", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if err := function(newRepositorySet(store.backend, transaction.Tx, transaction)); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return databaseError("commit storage transaction", err)
	}
	return nil
}

func waitForTransactionRetry(
	ctx context.Context,
	base time.Duration,
	attempt int,
) error {
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
