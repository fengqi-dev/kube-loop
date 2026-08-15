package storage

import (
	"errors"
	"time"
)

// MaximumManagementPageFetch is the largest page repositories accept. Callers
// may request one item beyond the public page size to detect truncation.
const MaximumManagementPageFetch = 101

func normalizePage(limit int, cursor *PageCursor) (int, *PageCursor, error) {
	if limit <= 0 || limit > MaximumManagementPageFetch {
		return 0, nil, errors.New("management list limit must be between 1 and 101")
	}
	normalized, err := normalizeCursor(cursor)
	return limit, normalized, err
}

func normalizeCursor(cursor *PageCursor) (*PageCursor, error) {
	if cursor == nil {
		return nil, nil
	}
	if cursor.CreatedAt.IsZero() || validateUUID(cursor.ID, "management page cursor ID") != nil {
		return nil, errors.New("management page cursor is invalid")
	}
	normalized := &PageCursor{CreatedAt: cursor.CreatedAt.UTC(), ID: cursor.ID}
	return normalized, nil
}

func appendPageBoundary(query string, arguments []any, columnPrefix string, cursor *PageCursor) (string, []any) {
	if cursor == nil {
		return query, arguments
	}
	createdColumn, idColumn := "created_at", "id"
	if columnPrefix != "" {
		createdColumn, idColumn = columnPrefix+".created_at", columnPrefix+".id"
	}
	query += " AND (" + createdColumn + " < ? OR (" + createdColumn + " = ? AND " + idColumn + " < ?))"
	formatted := formatTime(cursor.CreatedAt)
	return query, append(arguments, formatted, formatted, cursor.ID)
}

func pageCursor(createdAt time.Time, id string) *PageCursor {
	return &PageCursor{CreatedAt: createdAt.UTC(), ID: id}
}
