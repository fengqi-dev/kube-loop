package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

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
		if initialized == previousSchemaID {
			if err := migrateSchemaV1ToV2(ctx, transaction); err != nil {
				return err
			}
			initialized = currentSchemaID
		}
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

func migrateSchemaV1ToV2(ctx context.Context, transaction bun.Tx) error {
	trafficTypes := "('port-forward', 'exchange', 'mirror', 'preview')"
	if _, err := transaction.ExecContext(
		ctx,
		"DELETE FROM idempotency_records WHERE resource_type IN "+trafficTypes,
	); err != nil {
		return errors.New("remove legacy Traffic Session idempotency records")
	}
	if _, err := transaction.ExecContext(
		ctx,
		"DELETE FROM tasks WHERE type IN "+trafficTypes,
	); err != nil {
		return errors.New("remove legacy Traffic Session Tasks")
	}
	query := `UPDATE schema_metadata SET schema_id = ? WHERE id = 1`
	if _, err := transaction.ExecContext(ctx, query, currentSchemaID); err != nil {
		return errors.New("record storage schema migration")
	}
	return nil
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
