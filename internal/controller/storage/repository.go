package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/remotetask"
)

var (
	ErrNotFound            = errors.New("storage object not found")
	ErrConflict            = errors.New("storage object conflict")
	ErrIdempotencyMismatch = errors.New("idempotency key was used for a different request")
)

type PrincipalRepository interface {
	// Upsert preserves the stable ID and CreatedAt of an existing
	// (provider, external ID) identity while refreshing profile attributes.
	Upsert(context.Context, Principal) (Principal, error)
	GetByID(context.Context, string) (Principal, error)
	GetByIdentity(context.Context, string, string) (Principal, error)
}

type TokenFamilyRepository interface {
	// Create enforces unique IDs and refresh-token hashes.
	Create(context.Context, TokenFamily) error
	GetByID(context.Context, string) (TokenFamily, error)
	// Revoke is idempotent and never clears an earlier revocation timestamp.
	Revoke(context.Context, string, time.Time) error
	// RotateHash changes the current hash only when it still matches the
	// expected token, providing an optimistic guard for refresh rotation.
	RotateHash(context.Context, string, []byte, []byte) error
	// DeleteExpired performs bounded cleanup and returns the deleted row count.
	DeleteExpired(context.Context, time.Time, int) (int64, error)
}

type RefreshTokenRepository interface {
	Create(context.Context, RefreshTokenRecord) error
	GetByHash(context.Context, []byte) (RefreshTokenRecord, error)
	// MarkUsed atomically transitions active -> used and returns ErrConflict if
	// another request already consumed the token.
	MarkUsed(context.Context, []byte, time.Time) error
}

type SessionRepository interface {
	Create(context.Context, Session) error
	GetByID(context.Context, string) (Session, error)
	// UpdateState uses generation as an optimistic concurrency guard and returns
	// ErrConflict when the caller observed stale state.
	UpdateState(context.Context, string, uint64, string, time.Time) error
	// Heartbeat atomically refreshes the namespace NetworkSpec and extends an
	// active session only when generation still matches.
	Heartbeat(context.Context, string, uint64, json.RawMessage, string, time.Time, time.Time) error
	DeleteExpired(context.Context, time.Time, int) (int64, error)
}

type TaskRepository interface {
	// Create enforces one idempotency key per Principal.
	Create(context.Context, Task) error
	GetByID(context.Context, string) (Task, error)
	// UpdateState only changes the expected current state, preventing competing
	// workers from publishing two terminal outcomes.
	UpdateState(context.Context, string, remotetask.State, remotetask.State, json.RawMessage, time.Time) error
	ListBySession(context.Context, string, int) ([]Task, error)
	// ListStaleByTypeStates returns oldest Tasks whose owner heartbeat has not
	// advanced before the supplied time.
	ListStaleByTypeStates(context.Context, string, []remotetask.State, time.Time, int) ([]Task, error)
	// ClaimStale atomically changes a Task only when both its state and observed
	// update time still match, so at most one recovery worker owns the attempt.
	ClaimStale(context.Context, string, remotetask.State, time.Time, remotetask.State, json.RawMessage, time.Time) error
}

type ResourceSnapshotRepository interface {
	// Put replaces the snapshot for the same task/kind/namespace/name tuple.
	Put(context.Context, ResourceSnapshot) error
	ListByTask(context.Context, string) ([]ResourceSnapshot, error)
	DeleteByTask(context.Context, string) (int64, error)
}

type IdempotencyRepository interface {
	// Reserve atomically creates a key. Existing matching request hashes return
	// the stored record with created=false; mismatched hashes return
	// ErrIdempotencyMismatch.
	Reserve(context.Context, IdempotencyRecord) (IdempotencyRecord, bool, error)
	Get(context.Context, string, string) (IdempotencyRecord, error)
	DeleteExpired(context.Context, time.Time, int) (int64, error)
}

type AuditRepository interface {
	// Append is append-only. Audit events cannot be updated through this contract.
	Append(context.Context, AuditEvent) error
	List(context.Context, AuditFilter) ([]AuditEvent, error)
}

type AuthTransactionRepository interface {
	// CreateAttempt stores only a hash of the upstream OIDC state. ConsumeAttempt
	// atomically deletes and returns one unexpired attempt, preventing callback replay.
	CreateAttempt(context.Context, AuthAttempt) error
	ConsumeAttempt(context.Context, []byte, time.Time) (AuthAttempt, error)
	// Exchange codes are also persisted only as hashes and consumed exactly once.
	CreateExchange(context.Context, AuthExchange) error
	ConsumeExchange(context.Context, []byte, time.Time) (AuthExchange, error)
	DeleteExpired(context.Context, time.Time, int) (int64, error)
}

type Repositories interface {
	Principals() PrincipalRepository
	TokenFamilies() TokenFamilyRepository
	RefreshTokens() RefreshTokenRepository
	Sessions() SessionRepository
	Tasks() TaskRepository
	ResourceSnapshots() ResourceSnapshotRepository
	Idempotency() IdempotencyRepository
	Audit() AuditRepository
	AuthTransactions() AuthTransactionRepository
}

type TransactionManager interface {
	// WithinTransaction commits only when the callback succeeds and rolls back on
	// every error or panic. Repositories passed to the callback share one tx.
	// PostgreSQL transactions run at serializable isolation and may invoke the
	// callback again after a serialization failure or deadlock, so callbacks must
	// contain database work only and remain safe to retry.
	WithinTransaction(context.Context, func(Repositories) error) error
}
