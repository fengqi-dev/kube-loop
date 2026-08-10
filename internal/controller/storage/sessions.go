package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/uptrace/bun"
)

type sessionRepository struct {
	repositoryBase
}

type sessionRow struct {
	bun.BaseModel `bun:"table:sessions,alias:s"`

	ID              string          `bun:"id,pk"`
	SchemaVersion   int             `bun:"schema_version"`
	PrincipalID     string          `bun:"principal_id"`
	DeviceID        string          `bun:"device_id"`
	ClusterID       string          `bun:"cluster_id"`
	Namespace       string          `bun:"namespace"`
	State           string          `bun:"state"`
	Generation      int64           `bun:"generation"`
	NetworkSpec     json.RawMessage `bun:"network_spec_json,type:jsonb"`
	NetworkSpecHash string          `bun:"network_spec_hash"`
	CreatedAt       string          `bun:"created_at"`
	UpdatedAt       string          `bun:"updated_at"`
	LastHeartbeatAt string          `bun:"last_heartbeat_at"`
	ExpiresAt       string          `bun:"expires_at"`
}

func (repository *sessionRepository) Create(ctx context.Context, session Session) error {
	if err := normalizeSession(&session); err != nil {
		return err
	}
	row := rowFromSession(session)
	_, err := repository.orm.NewInsert().Model(&row).Exec(ctx)
	return mapWriteError(err)
}

func (repository *sessionRepository) GetByID(ctx context.Context, id string) (Session, error) {
	if err := validateUUID(id, "session ID"); err != nil {
		return Session{}, err
	}
	row := sessionRow{}
	err := repository.orm.NewSelect().Model(&row).Where("s.id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, databaseError("read session", err)
	}
	return sessionFromRow(row)
}

func (repository *sessionRepository) Heartbeat(
	ctx context.Context,
	id string,
	generation uint64,
	networkSpec json.RawMessage,
	networkSpecHash string,
	updatedAt, expiresAt time.Time,
) error {
	if err := validateUUID(id, "session ID"); err != nil {
		return err
	}
	if generation == 0 || generation >= math.MaxInt64 || updatedAt.IsZero() || !expiresAt.After(updatedAt) {
		return errors.New("session generation, heartbeat time and future expiry are required")
	}
	normalizedSpec, err := networkspec.Decode(networkSpec)
	if err != nil {
		return errors.New("session heartbeat NetworkSpec is invalid")
	}
	canonicalSpec, err := networkspec.CanonicalJSON(normalizedSpec)
	if err != nil {
		return errors.New("session heartbeat NetworkSpec is invalid")
	}
	canonicalHash, err := networkspec.Hash(normalizedSpec)
	if err != nil || canonicalHash != networkSpecHash {
		return errors.New("session heartbeat NetworkSpec hash is invalid")
	}
	result, err := repository.orm.NewUpdate().Model((*sessionRow)(nil)).
		Set("generation = generation + 1").Set("updated_at = ?", formatTime(updatedAt)).
		Set("last_heartbeat_at = ?", formatTime(updatedAt)).Set("expires_at = ?", formatTime(expiresAt)).
		Set("network_spec_json = ?", canonicalSpec).Set("network_spec_hash = ?", canonicalHash).
		Where("id = ?", id).Where("generation = ?", int64(generation)).Where("state = ?", "active").Exec(ctx)
	if err != nil {
		return mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil {
		return err
	}
	if count == 1 {
		return nil
	}
	if _, err := repository.GetByID(ctx, id); errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	return ErrConflict
}

func (repository *sessionRepository) UpdateState(ctx context.Context, id string, generation uint64, state string, updatedAt time.Time) error {
	if err := validateUUID(id, "session ID"); err != nil {
		return err
	}
	state = strings.TrimSpace(state)
	if state == "" || generation == 0 || generation >= math.MaxInt64 || updatedAt.IsZero() {
		return errors.New("session state, current generation and update time are required")
	}
	result, err := repository.orm.NewUpdate().Model((*sessionRow)(nil)).
		Set("state = ?", state).Set("generation = generation + 1").Set("updated_at = ?", formatTime(updatedAt)).
		Where("id = ?", id).Where("generation = ?", int64(generation)).Exec(ctx)
	if err != nil {
		return mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil {
		return err
	}
	if count == 1 {
		return nil
	}
	if _, err := repository.GetByID(ctx, id); errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	return ErrConflict
}

func (repository *sessionRepository) DeleteExpired(ctx context.Context, before time.Time, limit int) (int64, error) {
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
	if repository.backend == BackendPostgreSQL {
		query = `DELETE FROM sessions WHERE ctid IN (
			SELECT s.ctid FROM sessions AS s WHERE s.expires_at < $1 AND NOT EXISTS (
				SELECT 1 FROM tasks AS t INNER JOIN resource_snapshots AS r ON r.task_id = t.id
				WHERE t.session_id = s.id
			) ORDER BY s.expires_at LIMIT $2
		)`
	} else {
		query = repository.bind(query)
	}
	result, err := repository.executor.ExecContext(ctx, query, formatTime(before), limit)
	if err != nil {
		return 0, databaseError("delete expired sessions", err)
	}
	return rowsAffected(result)
}

func normalizeSession(session *Session) error {
	if err := validateUUID(session.ID, "session ID"); err != nil {
		return err
	}
	if err := validateUUID(session.PrincipalID, "principal ID"); err != nil {
		return err
	}
	session.DeviceID = strings.TrimSpace(session.DeviceID)
	session.ClusterID = strings.TrimSpace(session.ClusterID)
	session.Namespace = strings.TrimSpace(session.Namespace)
	session.State = strings.TrimSpace(session.State)
	if session.DeviceID == "" || session.ClusterID == "" || session.Namespace == "" || session.State == "" {
		return errors.New("session device, cluster, namespace and state are required")
	}
	if session.SchemaVersion == 0 {
		session.SchemaVersion = ObjectSchemaVersion
	}
	if session.SchemaVersion != ObjectSchemaVersion {
		return errors.New("unsupported session schema version")
	}
	if session.Generation == 0 {
		session.Generation = 1
	}
	if session.Generation >= math.MaxInt64 {
		return errors.New("session generation is too large")
	}
	if len(session.NetworkSpec) == 0 && session.NetworkSpecHash == "" {
		// Schema migrations expire legacy Sessions without a NetworkSpec. Keeping
		// them readable allows a clean lifecycle response, but they cannot issue a
		// RelayTicket or be created through the V2 API.
	} else {
		spec, err := networkspec.Decode(session.NetworkSpec)
		if err != nil {
			return err
		}
		contents, err := networkspec.CanonicalJSON(spec)
		if err != nil {
			return err
		}
		hash, err := networkspec.Hash(spec)
		if err != nil || session.NetworkSpecHash != hash {
			return errors.New("session NetworkSpec hash is invalid")
		}
		session.NetworkSpec = contents
	}
	if session.CreatedAt.IsZero() || session.ExpiresAt.IsZero() || !session.ExpiresAt.After(session.CreatedAt) {
		return errors.New("session expiry must be after creation")
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = session.CreatedAt
	}
	if session.LastHeartbeatAt.IsZero() {
		session.LastHeartbeatAt = session.CreatedAt
	}
	session.CreatedAt = session.CreatedAt.UTC()
	session.UpdatedAt = session.UpdatedAt.UTC()
	session.LastHeartbeatAt = session.LastHeartbeatAt.UTC()
	session.ExpiresAt = session.ExpiresAt.UTC()
	return nil
}

func rowFromSession(session Session) sessionRow {
	return sessionRow{
		ID: session.ID, SchemaVersion: session.SchemaVersion, PrincipalID: session.PrincipalID,
		DeviceID: session.DeviceID, ClusterID: session.ClusterID, Namespace: session.Namespace,
		State: session.State, Generation: int64(session.Generation), NetworkSpec: session.NetworkSpec,
		NetworkSpecHash: session.NetworkSpecHash, CreatedAt: formatTime(session.CreatedAt),
		UpdatedAt: formatTime(session.UpdatedAt), LastHeartbeatAt: formatTime(session.LastHeartbeatAt),
		ExpiresAt: formatTime(session.ExpiresAt),
	}
}

func sessionFromRow(row sessionRow) (Session, error) {
	if row.Generation < 1 {
		return Session{}, errors.New("decode session generation")
	}
	session := Session{
		ID: row.ID, SchemaVersion: row.SchemaVersion, PrincipalID: row.PrincipalID, DeviceID: row.DeviceID,
		ClusterID: row.ClusterID, Namespace: row.Namespace, State: row.State, Generation: uint64(row.Generation),
		NetworkSpec: append(json.RawMessage(nil), row.NetworkSpec...), NetworkSpecHash: row.NetworkSpecHash,
	}
	var err error
	if session.CreatedAt, err = parseTime(row.CreatedAt, "session creation time"); err != nil {
		return Session{}, err
	}
	if session.UpdatedAt, err = parseTime(row.UpdatedAt, "session update time"); err != nil {
		return Session{}, err
	}
	if session.LastHeartbeatAt, err = parseTime(row.LastHeartbeatAt, "session heartbeat time"); err != nil {
		return Session{}, err
	}
	if session.ExpiresAt, err = parseTime(row.ExpiresAt, "session expiry"); err != nil {
		return Session{}, err
	}
	if session.NetworkSpecHash != "" {
		spec, decodeErr := networkspec.Decode(session.NetworkSpec)
		if decodeErr != nil {
			return Session{}, errors.New("decode session NetworkSpec")
		}
		canonical, canonicalErr := networkspec.CanonicalJSON(spec)
		if canonicalErr != nil {
			return Session{}, errors.New("canonicalize session NetworkSpec")
		}
		hash, hashErr := networkspec.Hash(spec)
		if hashErr != nil || hash != session.NetworkSpecHash {
			return Session{}, errors.New("decode session NetworkSpec hash")
		}
		session.NetworkSpec = canonical
	} else {
		session.NetworkSpec = nil
	}
	return session, nil
}
