package storage

import (
	"context"
	"errors"
	"strings"
)

func (repository *identityRepository) List(
	ctx context.Context,
	filter IdentityListFilter,
) ([]Identity, error) {
	limit, cursor, err := normalizePage(filter.Limit, filter.Cursor)
	if err != nil {
		return nil, err
	}
	filter.Type = strings.TrimSpace(filter.Type)
	filter.Status = strings.TrimSpace(filter.Status)
	filter.Search = strings.TrimSpace(filter.Search)
	if filter.Type != "" && filter.Type != identityTypeHuman &&
		filter.Type != "machine" {
		return nil, errors.New("identity type filter is invalid")
	}
	if filter.Status != "" && !validIdentityStatus(filter.Status) {
		return nil, errors.New("identity status filter is invalid")
	}
	if len(filter.Search) > 256 ||
		strings.ContainsAny(filter.Search, "\x00\r\n") {
		return nil, errors.New("identity search filter is invalid")
	}
	query := `SELECT id, type, display_name, primary_email, status, created_at, updated_at FROM identities WHERE 1=1`
	arguments := make([]any, 0, 7)
	if filter.Type != "" {
		query += ` AND type = ?`
		arguments = append(arguments, filter.Type)
	}
	if filter.Status != "" {
		query += ` AND status = ?`
		arguments = append(arguments, filter.Status)
	}
	if filter.Search != "" {
		query += ` AND (LOWER(display_name) LIKE ? OR LOWER(primary_email) LIKE ?)`
		search := "%" + strings.ToLower(filter.Search) + "%"
		arguments = append(arguments, search, search)
	}
	query, arguments = appendPageBoundary(query, arguments, "", cursor)
	query += descendingPageSQL
	arguments = append(arguments, limit)
	rows, err := repository.executor.QueryContext(
		ctx,
		repository.bind(query),
		arguments...)
	if err != nil {
		return nil, databaseError("list identities", err)
	}
	defer func() { _ = rows.Close() }()
	identities := make([]Identity, 0)
	for rows.Next() {
		identity, scanErr := scanIdentity(rows)
		if scanErr != nil {
			return nil, databaseError("decode identity", scanErr)
		}
		identities = append(identities, identity)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate identities", err)
	}
	return identities, nil
}
