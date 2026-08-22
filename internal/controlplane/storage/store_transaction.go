package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

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
