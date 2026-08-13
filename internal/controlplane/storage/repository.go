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
	RevokeAuthorization(context.Context, string, time.Time) error
	DeleteExpired(context.Context, time.Time, int) (int64, error)
}

type OAuthClientRepository interface {
	Create(context.Context, OAuthClient) error
	Get(context.Context, string) (OAuthClient, error)
	List(context.Context) ([]OAuthClient, error)
	Update(context.Context, OAuthClient) error
	Delete(context.Context, string) error
	SetSecret(context.Context, OAuthClientSecret) error
	GetSecret(context.Context, string) (OAuthClientSecret, error)
}

type OAuthSessionRepository interface {
	Create(context.Context, OAuthSession) error
	Get(context.Context, string, []byte) (OAuthSession, error)
	Consume(context.Context, string, []byte, time.Time) (OAuthSession, error)
	Delete(context.Context, string, []byte) error
	RevokeRequest(context.Context, string, time.Time) error
	RevokePrincipal(context.Context, string, time.Time) (int64, error)
	RequestOwner(context.Context, string) (string, string, error)
	RequestActive(context.Context, string, time.Time) (bool, error)
	DeleteExpired(context.Context, time.Time, int) (int64, error)
}

type OAuthAuthorizationRequestRepository interface {
	Create(context.Context, OAuthAuthorizationRequest) error
	Get(context.Context, []byte, time.Time) (OAuthAuthorizationRequest, error)
	Consume(context.Context, []byte, time.Time) (OAuthAuthorizationRequest, error)
	SetUpstream(context.Context, []byte, []byte, json.RawMessage, string, time.Time) error
	ConsumeUpstream(context.Context, []byte, time.Time) (OAuthAuthorizationRequest, error)
	Continue(context.Context, []byte, []byte, []byte, string, time.Time) error
	DeleteExpired(context.Context, time.Time, int) (int64, error)
}

type OAuthConsentRepository interface {
	Grant(context.Context, OAuthConsent) error
	Has(context.Context, string, string, []byte) (bool, error)
	RevokeClient(context.Context, string, string) error
}

type OAuthBrowserSessionRepository interface {
	Create(context.Context, OAuthBrowserSession) error
	Get(context.Context, []byte, time.Time) (OAuthBrowserSession, error)
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

type AuthorizationDefinitionRepository interface {
	CreateRole(context.Context, AuthorizationRoleRecord) error
	CreateBinding(context.Context, AuthorizationBindingRecord) error
}

type ProviderConfigRevisionRepository interface {
	Create(context.Context, ProviderConfigRevision) (ProviderConfigRevision, error)
	Get(context.Context, uint64) (ProviderConfigRevision, error)
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
	Sessions() SessionRepository
	Tasks() TaskRepository
	ResourceSnapshots() ResourceSnapshotRepository
	Idempotency() IdempotencyRepository
	Audit() AuditRepository
	RelayDesiredStates() RelayDesiredStateRepository
	AuditExportJobs() AuditExportJobRepository
	ManagementState() ManagementStateRepository
	AdminSessions() AdminSessionRepository
	LocalAdminUsers() LocalAdminUserRepository
	AdminRecoveryCodes() AdminRecoveryCodeRepository
	AdminPolicyRevisions() AdminPolicyRevisionRepository
	AuthorizationDefinitions() AuthorizationDefinitionRepository
	ProviderConfigRevisions() ProviderConfigRevisionRepository
	ActiveManagementRevisions() ActiveManagementRevisionRepository
	ConfigChangeRequests() ConfigChangeRequestRepository
	OAuthClients() OAuthClientRepository
	OAuthSessions() OAuthSessionRepository
	OAuthConsents() OAuthConsentRepository
	OAuthAuthorizationRequests() OAuthAuthorizationRequestRepository
	OAuthBrowserSessions() OAuthBrowserSessionRepository
}

type TransactionManager interface {
	// WithinTransaction commits only when the callback succeeds and rolls back on
	// every error or panic. Repositories passed to the callback share one tx.
	// PostgreSQL transactions run at serializable isolation and may invoke the
	// callback again after a serialization failure or deadlock, so callbacks must
	// contain database work only and remain safe to retry.
	WithinTransaction(context.Context, func(Repositories) error) error
}

type RepositoryTransaction interface {
	Repositories() Repositories
	Commit() error
	Rollback() error
}

type ExplicitTransactionManager interface {
	BeginTransaction(context.Context) (RepositoryTransaction, error)
}
