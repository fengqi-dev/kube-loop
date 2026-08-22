package storage

import (
	"context"
	"errors"
	"strings"
)

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
