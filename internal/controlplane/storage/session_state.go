package storage

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

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
	if generation == 0 || generation >= math.MaxInt64 || updatedAt.IsZero() ||
		!expiresAt.After(updatedAt) {
		return errors.New(
			"session generation, heartbeat time and future expiry are required",
		)
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
		Set("generation = generation + 1").
		Set("updated_at = ?", formatTime(updatedAt)).
		Set("last_heartbeat_at = ?", formatTime(updatedAt)).
		Set("expires_at = ?", formatTime(expiresAt)).
		Set("network_spec_json = ?", string(canonicalSpec)).
		Set("network_spec_hash = ?", canonicalHash).
		Where("id = ?", id).
		Where("generation = ?", int64(generation)).
		Where("state = ?", statusActive).Exec(ctx)
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

func (repository *sessionRepository) UpdateState(
	ctx context.Context,
	id string,
	generation uint64,
	state string,
	updatedAt time.Time,
) error {
	if err := validateUUID(id, "session ID"); err != nil {
		return err
	}
	state = strings.TrimSpace(state)
	if state == "" || generation == 0 || generation >= math.MaxInt64 ||
		updatedAt.IsZero() {
		return errors.New(
			"session state, current generation and update time are required",
		)
	}
	result, err := repository.orm.NewUpdate().Model((*sessionRow)(nil)).
		Set("state = ?", state).
		Set("generation = generation + 1").
		Set("updated_at = ?", formatTime(updatedAt)).
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
