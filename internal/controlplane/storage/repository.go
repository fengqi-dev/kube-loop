package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
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
	List(context.Context, PrincipalListFilter) ([]Principal, error)
}

type TokenFamilyRepository interface {
	// Create enforces unique IDs and refresh-token hashes.
	Create(context.Context, TokenFamily) error
	GetByID(context.Context, string) (TokenFamily, error)
	// Revoke is idempotent and never clears an earlier revocation timestamp.
	Revoke(context.Context, string, time.Time) error
	// RevokeByPrincipal atomically revokes every active Device Session owned by
	// one Principal and returns the number newly revoked.
	RevokeByPrincipal(context.Context, string, time.Time) (int64, error)
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
	List(context.Context, SessionListFilter) ([]Session, error)
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
	List(context.Context, TaskListFilter) ([]Task, error)
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

type RelayDesiredStateRepository interface {
	Get(context.Context, string) (RelayDesiredState, error)
	List(context.Context) ([]RelayDesiredState, error)
	// CompareAndSwap creates with expectedVersion=0 or advances an existing
	// monotonic version. A stale expected version returns ErrConflict.
	CompareAndSwap(context.Context, string, string, uint64, string, string, string, time.Time) (RelayDesiredState, error)
}

type AuditExportJobRepository interface {
	Create(context.Context, AuditExportJob) error
	GetByID(context.Context, string) (AuditExportJob, error)
	ListRunnable(context.Context, time.Time, int) ([]AuditExportJob, error)
	Claim(context.Context, string, time.Time, time.Time, time.Time) error
	Complete(context.Context, string, string, string, string, time.Time) error
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

type ManagementStateRepository interface {
	// BootstrapRetired fails closed at the authorization layer when storage is
	// unavailable. A missing singleton row means bootstrap has never retired.
	BootstrapRetired(context.Context) (bool, error)
	// RetireBootstrap is irreversible through the application repository. It is
	// idempotent and returns true only when this call persisted the marker.
	RetireBootstrap(context.Context, uint64, time.Time) (bool, error)
}

type AdminSessionRepository interface {
	Create(context.Context, AdminSession) error
	GetByHash(context.Context, []byte) (AdminSession, error)
	// Touch uses the observed last-seen timestamp as an optimistic guard and
	// refuses revoked or already expired sessions.
	Touch(context.Context, []byte, time.Time, time.Time, time.Time, time.Time) error
	Revoke(context.Context, []byte, time.Time) error
	DeleteExpired(context.Context, time.Time, int) (int64, error)
}

type LocalAdminUserRepository interface {
	Create(context.Context, LocalAdminUser) error
	GetByPrincipalID(context.Context, string) (LocalAdminUser, error)
	GetByUsername(context.Context, string) (LocalAdminUser, error)
	List(context.Context) ([]LocalAdminUser, error)
	UpdatePassword(context.Context, string, string, time.Time) error
	UpdateEnabled(context.Context, string, bool, time.Time) error
	UpdateTOTP(context.Context, string, []byte, time.Time) error
	MarkBootstrapComplete(context.Context, string, time.Time) error
}

type AdminRecoveryCodeRepository interface {
	Replace(context.Context, string, [][]byte, time.Time) error
	Consume(context.Context, string, []byte) error
	DeleteByPrincipal(context.Context, string) error
}

type AdminPolicyRevisionRepository interface {
	Create(context.Context, AdminPolicyRevision) (AdminPolicyRevision, error)
	Get(context.Context, uint64) (AdminPolicyRevision, error)
}

type ProviderConfigRevisionRepository interface {
	Create(context.Context, ProviderConfigRevision) (ProviderConfigRevision, error)
	Get(context.Context, uint64) (ProviderConfigRevision, error)
}

type AdminAssignmentRepository interface {
	Create(context.Context, AdminAssignment) error
	ListByPolicyRevision(context.Context, uint64) ([]AdminAssignment, error)
}

type ActiveManagementRevisionRepository interface {
	Get(context.Context, string, string) (ActiveManagementRevision, error)
	List(context.Context, string) ([]ActiveManagementRevision, error)
	// CompareAndSwap changes an active pointer only when its monotonic ETag still
	// matches. expectedETag=0 creates the first pointer; rollback never lowers ETag.
	CompareAndSwap(context.Context, string, string, uint64, uint64, string, string, time.Time) (ActiveManagementRevision, error)
}

type ConfigChangeRequestRepository interface {
	Create(context.Context, ConfigChangeRequest) error
	GetByID(context.Context, string) (ConfigChangeRequest, error)
	GetByIdempotencyHash(context.Context, string, string, string, string, []byte) (ConfigChangeRequest, error)
	UpdateStatus(context.Context, string, string, string, json.RawMessage, time.Time) error
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
	RelayDesiredStates() RelayDesiredStateRepository
	AuditExportJobs() AuditExportJobRepository
	AuthTransactions() AuthTransactionRepository
	ManagementState() ManagementStateRepository
	AdminSessions() AdminSessionRepository
	LocalAdminUsers() LocalAdminUserRepository
	AdminRecoveryCodes() AdminRecoveryCodeRepository
	AdminPolicyRevisions() AdminPolicyRevisionRepository
	ProviderConfigRevisions() ProviderConfigRevisionRepository
	AdminAssignments() AdminAssignmentRepository
	ActiveManagementRevisions() ActiveManagementRevisionRepository
	ConfigChangeRequests() ConfigChangeRequestRepository
}

type TransactionManager interface {
	// WithinTransaction commits only when the callback succeeds and rolls back on
	// every error or panic. Repositories passed to the callback share one tx.
	// PostgreSQL transactions run at serializable isolation and may invoke the
	// callback again after a serialization failure or deadlock, so callbacks must
	// contain database work only and remain safe to retry.
	WithinTransaction(context.Context, func(Repositories) error) error
}
