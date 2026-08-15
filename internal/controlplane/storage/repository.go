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

type IdentityRepository interface {
	Create(context.Context, Identity) (Identity, error)
	Update(context.Context, Identity) error
	GetByID(context.Context, string) (Identity, error)
	List(context.Context, IdentityListFilter) ([]Identity, error)
}

type OrganizationRepository interface {
	Create(context.Context, Organization) error
	Get(context.Context, string) (Organization, error)
	List(context.Context, int) ([]Organization, error)
	AddMember(context.Context, OrganizationMembership) error
	RemoveMember(context.Context, string, string) error
	ListMembers(context.Context, string, int) ([]OrganizationMembership, error)
	ListForIdentity(context.Context, string) ([]Organization, error)
}

type GroupRepository interface {
	Create(context.Context, Group) error
	Get(context.Context, string) (Group, error)
	List(context.Context, string, int) ([]Group, error)
	Update(context.Context, Group) error
	Delete(context.Context, string) error
	AddMember(context.Context, GroupMembership) error
	RemoveMember(context.Context, string, string) error
	ListMembers(context.Context, string, int) ([]GroupMembership, error)
	ListForIdentity(context.Context, string, string) ([]Group, error)
	PutNamespace(context.Context, GroupNamespace) error
	DeleteNamespace(context.Context, string, string) error
	ListNamespaces(context.Context, string) ([]GroupNamespace, error)
	ListAuthorizedNamespaces(context.Context, string, []string) ([]string, error)
	IsAdministrator(context.Context, string, []string) (bool, error)
}

type InvitationRepository interface {
	Create(context.Context, Invitation) error
	GetByTokenHash(context.Context, []byte, time.Time) (Invitation, error)
	List(context.Context, string, int) ([]Invitation, error)
	Accept(context.Context, string, string, time.Time) error
	Revoke(context.Context, string, time.Time) error
}

type BootstrapTokenRepository interface {
	Create(context.Context, BootstrapToken) error
	Get(context.Context) (BootstrapToken, error)
	Consume(context.Context, []byte, time.Time) error
}

type CredentialRepository interface {
	CreatePassword(context.Context, PasswordCredential) error
	GetPasswordByIdentity(context.Context, string) (PasswordCredential, error)
	GetPasswordByUsername(context.Context, string) (PasswordCredential, error)
	UpdatePassword(context.Context, string, string, time.Time) error
	SetPasswordEnabled(context.Context, string, bool, time.Time) error
}

type SecurityPolicyRepository interface {
	Put(context.Context, SecurityPolicy, uint64) (SecurityPolicy, error)
	GetPlatform(context.Context) (SecurityPolicy, error)
	GetOrganization(context.Context, string) (SecurityPolicy, error)
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
	// Create enforces one idempotency key per Identity.
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
	ListGrants(context.Context, OAuthGrantListFilter) ([]OAuthGrant, error)
	Get(context.Context, string, []byte) (OAuthSession, error)
	Consume(context.Context, string, []byte, time.Time) (OAuthSession, error)
	Delete(context.Context, string, []byte) error
	RevokeRequest(context.Context, string, time.Time) error
	RevokeIdentity(context.Context, string, time.Time) (int64, error)
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

type Repositories interface {
	Identities() IdentityRepository
	Organizations() OrganizationRepository
	Groups() GroupRepository
	Invitations() InvitationRepository
	BootstrapTokens() BootstrapTokenRepository
	Credentials() CredentialRepository
	SecurityPolicies() SecurityPolicyRepository
	Sessions() SessionRepository
	Tasks() TaskRepository
	ResourceSnapshots() ResourceSnapshotRepository
	Idempotency() IdempotencyRepository
	Audit() AuditRepository
	RelayDesiredStates() RelayDesiredStateRepository
	AuditExportJobs() AuditExportJobRepository
	AdminSessions() AdminSessionRepository
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
