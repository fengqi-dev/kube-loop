package storage

import (
	"context"
	"database/sql"
	"time"
)

func (repository *sessionRepository) DeleteExpired(
	ctx context.Context,
	before time.Time,
	limit int,
) (int64, error) {
	limit, err := boundedLimit(limit)
	if err != nil {
		return 0, err
	}
	query := `DELETE FROM sessions WHERE rowid IN (
		SELECT s.rowid FROM sessions AS s WHERE s.expires_at < ? AND NOT EXISTS (
			SELECT 1 FROM tasks AS t INNER JOIN resource_snapshots AS r ON r.task_id = t.id
			WHERE t.session_id = s.id
		) ORDER BY s.expires_at LIMIT ?
	)`
	switch repository.backend {
	case BackendSQLite:
		query = repository.bind(query)
	case BackendPostgreSQL:
		query = `DELETE FROM sessions WHERE ctid IN (
			SELECT s.ctid FROM sessions AS s WHERE s.expires_at < $1 AND NOT EXISTS (
				SELECT 1 FROM tasks AS t INNER JOIN resource_snapshots AS r ON r.task_id = t.id
				WHERE t.session_id = s.id
			) ORDER BY s.expires_at LIMIT $2
		)`
	case BackendMySQL:
		return repository.deleteExpiredMySQL(ctx, before, limit)
	}
	result, err := repository.executor.ExecContext(
		ctx,
		query,
		formatTime(before),
		limit,
	)
	if err != nil {
		return 0, databaseError("delete expired sessions", err)
	}
	return rowsAffected(result)
}

// MySQL ignores the inline REFERENCES clauses used by the portable baseline
// schema, so deleting a Session does not cascade to its Tasks. Delete both in
// one serializable transaction while retaining the rollback-snapshot guard.
func (repository *sessionRepository) deleteExpiredMySQL(
	ctx context.Context,
	before time.Time,
	limit int,
) (int64, error) {
	transaction, err := repository.orm.BeginTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return 0, databaseError("begin expired Session cleanup", err)
	}
	defer func() { _ = transaction.Rollback() }()

	rows, err := transaction.QueryContext(ctx, `SELECT s.id
		FROM sessions AS s
		WHERE s.expires_at < ? AND NOT EXISTS (
			SELECT 1 FROM tasks AS t INNER JOIN resource_snapshots AS r ON r.task_id = t.id
			WHERE t.session_id = s.id
		)
		ORDER BY s.expires_at, s.id
		LIMIT ? FOR UPDATE`, formatTime(before), limit)
	if err != nil {
		return 0, databaseError("select expired Sessions", err)
	}
	defer func() { _ = rows.Close() }()
	identifiers := make([]string, 0, limit)
	for rows.Next() {
		var identifier string
		if err := rows.Scan(&identifier); err != nil {
			return 0, databaseError("scan expired Session", err)
		}
		identifiers = append(identifiers, identifier)
	}
	if err := rows.Close(); err != nil {
		return 0, databaseError("close expired Session rows", err)
	}
	if err := rows.Err(); err != nil {
		return 0, databaseError("iterate expired Sessions", err)
	}

	var deleted int64
	for _, identifier := range identifiers {
		if _, err := transaction.ExecContext(
			ctx,
			`DELETE FROM tasks WHERE session_id = ?`,
			identifier,
		); err != nil {
			return 0, databaseError("delete expired Session Tasks", err)
		}
		result, err := transaction.ExecContext(
			ctx,
			`DELETE FROM sessions WHERE id = ?`,
			identifier,
		)
		if err != nil {
			return 0, databaseError("delete expired Session", err)
		}
		count, err := rowsAffected(result)
		if err != nil {
			return 0, err
		}
		deleted += count
	}
	if err := transaction.Commit(); err != nil {
		return 0, databaseError("commit expired Session cleanup", err)
	}
	return deleted, nil
}
