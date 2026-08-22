package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

type sessionRepository struct {
	repositoryBase
}

func (repository *sessionRepository) Create(
	ctx context.Context,
	session Session,
) error {
	if err := normalizeSession(&session); err != nil {
		return err
	}
	row := rowFromSession(session)
	_, err := repository.orm.NewInsert().Model(&row).Exec(ctx)
	return mapWriteError(err)
}

func (repository *sessionRepository) GetByID(
	ctx context.Context,
	id string,
) (Session, error) {
	if err := validateUUID(id, "session ID"); err != nil {
		return Session{}, err
	}
	row := sessionRow{}
	err := repository.orm.NewSelect().
		Model(&row).
		Where("s.id = ?", id).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, databaseError("read session", err)
	}
	return sessionFromRow(row)
}

func (repository *sessionRepository) List(
	ctx context.Context,
	filter SessionListFilter,
) ([]Session, error) {
	limit, cursor, err := normalizePage(filter.Limit, filter.Cursor)
	if err != nil {
		return nil, err
	}
	filter.IdentityID = strings.TrimSpace(filter.IdentityID)
	filter.Namespace = strings.TrimSpace(filter.Namespace)
	filter.State = strings.TrimSpace(filter.State)
	if filter.IdentityID != "" &&
		validateUUID(filter.IdentityID, "session identity ID") != nil {
		return nil, errors.New("session identity filter is invalid")
	}
	if filter.Namespace != "" && !dns1123Label.MatchString(filter.Namespace) {
		return nil, errors.New("session namespace filter is invalid")
	}
	if len(filter.State) > 64 || strings.ContainsAny(filter.State, "\x00\r\n") {
		return nil, errors.New("session state filter is invalid")
	}
	query := `SELECT id, identity_id, device_id, cluster_id, namespace, state, generation,
		network_spec_json, network_spec_hash, created_at, updated_at, last_heartbeat_at, expires_at
		FROM sessions WHERE 1=1`
	arguments := make([]any, 0, 9)
	if filter.IdentityID != "" {
		query += identityFilterSQL
		arguments = append(arguments, filter.IdentityID)
	}
	if filter.Namespace != "" {
		query += ` AND namespace = ?`
		arguments = append(arguments, filter.Namespace)
	}
	if filter.State != "" {
		query += ` AND state = ?`
		arguments = append(arguments, filter.State)
	}
	query, arguments = appendPageBoundary(query, arguments, "", cursor)
	query += descendingPageSQL
	arguments = append(arguments, limit)
	rows, err := repository.executor.QueryContext(
		ctx,
		repository.bind(query),
		arguments...)
	if err != nil {
		return nil, databaseError("list sessions", err)
	}
	defer func() { _ = rows.Close() }()
	sessions := make([]Session, 0)
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate sessions", err)
	}
	return sessions, nil
}
