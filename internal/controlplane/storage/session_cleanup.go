package storage

import (
	"context"
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
		query = `DELETE FROM sessions WHERE expires_at < ? AND NOT EXISTS (
			SELECT 1 FROM tasks AS t INNER JOIN resource_snapshots AS r ON r.task_id = t.id
			WHERE t.session_id = sessions.id
		) ORDER BY expires_at LIMIT ?`
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
