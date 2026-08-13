package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type principalRepository struct {
	repositoryBase
}

func (repository *principalRepository) Upsert(ctx context.Context, principal Principal) (Principal, error) {
	if err := normalizePrincipal(&principal); err != nil {
		return Principal{}, err
	}
	groups, err := json.Marshal(principal.Groups)
	if err != nil {
		return Principal{}, errors.New("encode principal groups")
	}
	query := `INSERT INTO principals(
		id, schema_version, provider, external_id, display_name, email, groups_json, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(provider, external_id) DO UPDATE SET
		schema_version=excluded.schema_version,
		display_name=excluded.display_name,
		email=excluded.email,
		groups_json=excluded.groups_json,
		updated_at=excluded.updated_at
	RETURNING id, schema_version, provider, external_id, display_name, email, groups_json, created_at, updated_at`
	if repository.backend == BackendPostgreSQL {
		query = `INSERT INTO principals(
			id, schema_version, provider, external_id, display_name, email, groups_json, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9)
		ON CONFLICT(provider, external_id) DO UPDATE SET
			schema_version=excluded.schema_version,
			display_name=excluded.display_name,
			email=excluded.email,
			groups_json=excluded.groups_json,
			updated_at=excluded.updated_at
		RETURNING id, schema_version, provider, external_id, display_name, email, groups_json, created_at, updated_at`
	}
	if repository.backend == BackendMySQL {
		query = `INSERT INTO principals(
			id, schema_version, provider, external_id, display_name, email, groups_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			schema_version=VALUES(schema_version), display_name=VALUES(display_name),
			email=VALUES(email), groups_json=VALUES(groups_json), updated_at=VALUES(updated_at)`
		if _, err := repository.executor.ExecContext(ctx, query,
			principal.ID, principal.SchemaVersion, principal.Provider, principal.ExternalID,
			principal.DisplayName, principal.Email, string(groups), formatTime(principal.CreatedAt), formatTime(principal.UpdatedAt),
		); err != nil {
			return Principal{}, mapWriteError(err)
		}
		return repository.GetByIdentity(ctx, principal.Provider, principal.ExternalID)
	}
	row := repository.executor.QueryRowContext(ctx, query,
		principal.ID, principal.SchemaVersion, principal.Provider, principal.ExternalID,
		principal.DisplayName, principal.Email, string(groups), formatTime(principal.CreatedAt), formatTime(principal.UpdatedAt),
	)
	result, err := scanPrincipal(row)
	if err != nil {
		if isConstraintError(err) {
			return Principal{}, ErrConflict
		}
		return Principal{}, databaseError("store principal", err)
	}
	return result, nil
}

func (repository *principalRepository) GetByID(ctx context.Context, id string) (Principal, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Principal{}, errors.New("principal ID must be a UUID")
	}
	query := `SELECT id, schema_version, provider, external_id, display_name, email, groups_json, created_at, updated_at
		FROM principals WHERE id = ?`
	principal, err := scanPrincipal(repository.executor.QueryRowContext(ctx, repository.bind(query), id))
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrNotFound
	}
	if err != nil {
		return Principal{}, databaseError("read principal", err)
	}
	return principal, nil
}

func (repository *principalRepository) GetByIdentity(ctx context.Context, provider, externalID string) (Principal, error) {
	provider = strings.TrimSpace(provider)
	externalID = strings.TrimSpace(externalID)
	if provider == "" || externalID == "" {
		return Principal{}, errors.New("provider and external ID are required")
	}
	query := `SELECT id, schema_version, provider, external_id, display_name, email, groups_json, created_at, updated_at
		FROM principals WHERE provider = ? AND external_id = ?`
	principal, err := scanPrincipal(repository.executor.QueryRowContext(ctx, repository.bind(query), provider, externalID))
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrNotFound
	}
	if err != nil {
		return Principal{}, databaseError("read principal identity", err)
	}
	return principal, nil
}

func (repository *principalRepository) List(ctx context.Context, filter PrincipalListFilter) ([]Principal, error) {
	limit, cursor, err := normalizePage(filter.Limit, filter.Cursor)
	if err != nil {
		return nil, err
	}
	filter.Provider = strings.TrimSpace(filter.Provider)
	if len(filter.Provider) > 128 || strings.ContainsAny(filter.Provider, "\x00\r\n") {
		return nil, errors.New("principal provider filter is invalid")
	}
	query := `SELECT id, schema_version, provider, external_id, display_name, email, groups_json, created_at, updated_at
		FROM principals WHERE 1=1`
	arguments := make([]any, 0, 5)
	if filter.Provider != "" {
		query += ` AND provider = ?`
		arguments = append(arguments, filter.Provider)
	}
	query, arguments = appendPageBoundary(query, arguments, "", cursor)
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	arguments = append(arguments, limit)
	rows, err := repository.executor.QueryContext(ctx, repository.bind(query), arguments...)
	if err != nil {
		return nil, databaseError("list principals", err)
	}
	defer rows.Close()
	principals := make([]Principal, 0)
	for rows.Next() {
		principal, err := scanPrincipal(rows)
		if err != nil {
			return nil, err
		}
		principals = append(principals, principal)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate principals", err)
	}
	return principals, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanPrincipal(row rowScanner) (Principal, error) {
	var (
		principal       Principal
		groups          []byte
		createdAtString string
		updatedAtString string
	)
	if err := row.Scan(
		&principal.ID, &principal.SchemaVersion, &principal.Provider, &principal.ExternalID,
		&principal.DisplayName, &principal.Email, &groups, &createdAtString, &updatedAtString,
	); err != nil {
		return Principal{}, err
	}
	if err := json.Unmarshal(groups, &principal.Groups); err != nil {
		return Principal{}, errors.New("decode principal groups")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtString)
	if err != nil {
		return Principal{}, errors.New("decode principal creation time")
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updatedAtString)
	if err != nil {
		return Principal{}, errors.New("decode principal update time")
	}
	principal.CreatedAt = createdAt
	principal.UpdatedAt = updatedAt
	return principal, nil
}

func normalizePrincipal(principal *Principal) error {
	if _, err := uuid.Parse(principal.ID); err != nil {
		return errors.New("principal ID must be a UUID")
	}
	principal.Provider = strings.TrimSpace(principal.Provider)
	principal.ExternalID = strings.TrimSpace(principal.ExternalID)
	principal.DisplayName = strings.TrimSpace(principal.DisplayName)
	principal.Email = strings.TrimSpace(principal.Email)
	if principal.Provider == "" || principal.ExternalID == "" {
		return errors.New("principal provider and external ID are required")
	}
	if principal.SchemaVersion == 0 {
		principal.SchemaVersion = ObjectSchemaVersion
	}
	if principal.SchemaVersion != ObjectSchemaVersion {
		return fmt.Errorf("unsupported principal schema version %d", principal.SchemaVersion)
	}
	if principal.CreatedAt.IsZero() {
		principal.CreatedAt = time.Now().UTC()
	}
	if principal.UpdatedAt.IsZero() {
		principal.UpdatedAt = principal.CreatedAt
	}
	principal.CreatedAt = principal.CreatedAt.UTC()
	principal.UpdatedAt = principal.UpdatedAt.UTC()
	if principal.Groups == nil {
		principal.Groups = []string{}
	}
	return nil
}

func isConstraintError(err error) bool {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && strings.HasPrefix(postgresError.Code, "23") {
		return true
	}
	var mysqlError *mysqldriver.MySQLError
	if errors.As(err, &mysqlError) && (mysqlError.Number == 1062 || mysqlError.Number == 1451 || mysqlError.Number == 1452 || mysqlError.Number == 3819) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "constraint failed")
}
