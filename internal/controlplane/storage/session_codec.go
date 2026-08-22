package storage

import (
	"encoding/json"
	"errors"
	"math"
	"strings"

	"github.com/uptrace/bun"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

type sessionRow struct {
	bun.BaseModel `bun:"table:sessions,alias:s"`

	ID              string          `bun:"id,pk"`
	IdentityID      string          `bun:"identity_id"`
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

func scanSession(row rowScanner) (Session, error) {
	var value sessionRow
	var networkSpec []byte
	if err := row.Scan(
		&value.ID, &value.IdentityID, &value.DeviceID, &value.ClusterID,
		&value.Namespace, &value.State, &value.Generation, &networkSpec, &value.NetworkSpecHash,
		&value.CreatedAt, &value.UpdatedAt, &value.LastHeartbeatAt, &value.ExpiresAt,
	); err != nil {
		return Session{}, err
	}
	value.NetworkSpec = append(json.RawMessage(nil), networkSpec...)
	return sessionFromRow(value)
}

func normalizeSession(session *Session) error {
	if err := validateUUID(session.ID, "session ID"); err != nil {
		return err
	}
	if err := validateUUID(session.IdentityID, "identity ID"); err != nil {
		return err
	}
	session.DeviceID = strings.TrimSpace(session.DeviceID)
	session.ClusterID = strings.TrimSpace(session.ClusterID)
	session.Namespace = strings.TrimSpace(session.Namespace)
	session.State = strings.TrimSpace(session.State)
	if session.DeviceID == "" || session.ClusterID == "" ||
		session.Namespace == "" ||
		session.State == "" {
		return errors.New(
			"session device, cluster, namespace and state are required",
		)
	}
	if session.Generation == 0 {
		session.Generation = 1
	}
	if session.Generation >= math.MaxInt64 {
		return errors.New("session generation is too large")
	}
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
	if session.CreatedAt.IsZero() || session.ExpiresAt.IsZero() ||
		!session.ExpiresAt.After(session.CreatedAt) {
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
		ID: session.ID, IdentityID: session.IdentityID,
		DeviceID: session.DeviceID, ClusterID: session.ClusterID, Namespace: session.Namespace,
		//nolint:gosec // normalizeSession rejects generations that do not fit in the signed database column.
		State: session.State, Generation: int64(session.Generation), NetworkSpec: session.NetworkSpec,
		NetworkSpecHash: session.NetworkSpecHash, CreatedAt: formatTime(session.CreatedAt),
		UpdatedAt: formatTime(
			session.UpdatedAt,
		), LastHeartbeatAt: formatTime(session.LastHeartbeatAt),
		ExpiresAt: formatTime(session.ExpiresAt),
	}
}

func sessionFromRow(row sessionRow) (Session, error) {
	if row.Generation < 1 {
		return Session{}, errors.New("decode session generation")
	}
	session := Session{
		ID: row.ID, IdentityID: row.IdentityID, DeviceID: row.DeviceID,
		ClusterID: row.ClusterID, Namespace: row.Namespace, State: row.State, Generation: uint64(row.Generation),
		NetworkSpec: append(
			json.RawMessage(nil),
			row.NetworkSpec...), NetworkSpecHash: row.NetworkSpecHash,
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
	return session, nil
}
