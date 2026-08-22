package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/mysqldialect"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/dialect/sqlitedialect"
)

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
