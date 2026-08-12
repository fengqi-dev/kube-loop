package storage

import (
	"encoding/json"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

const ObjectSchemaVersion = 1

type Principal struct {
	ID            string
	SchemaVersion int
	Provider      string
	ExternalID    string
	DisplayName   string
	Email         string
	Groups        []string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type PageCursor struct {
	CreatedAt time.Time
	ID        string
}

type PrincipalListFilter struct {
	Provider string
	Cursor   *PageCursor
	Limit    int
}

type SessionListFilter struct {
	PrincipalID string
	Namespace   string
	State       string
	Cursor      *PageCursor
	Limit       int
}

type TaskListFilter struct {
	PrincipalID string
	SessionID   string
	Namespace   string
	Type        string
	State       remotetask.State
	Cursor      *PageCursor
	Limit       int
}

// DeviceSession is the server-side authentication session for one stable
// client device. Refresh-token rotation records are children of this aggregate.
type DeviceSession struct {
	ID               string
	SchemaVersion    int
	PrincipalID      string
	DeviceID         string
	RefreshTokenHash []byte
	CreatedAt        time.Time
	ExpiresAt        time.Time
	RevokedAt        *time.Time
}

// TokenFamily is retained as the repository/API name for refresh-token logic.
type TokenFamily = DeviceSession

type RefreshTokenRecord struct {
	TokenHash []byte
	FamilyID  string
	Status    string
	CreatedAt time.Time
	UsedAt    *time.Time
}

// ClusterSession owns one principal/device/cluster/namespace runtime scope.
type ClusterSession struct {
	ID              string
	SchemaVersion   int
	PrincipalID     string
	DeviceID        string
	ClusterID       string
	Namespace       string
	State           string
	Generation      uint64
	NetworkSpec     json.RawMessage
	NetworkSpecHash string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LastHeartbeatAt time.Time
	ExpiresAt       time.Time
}

// Session is retained as the repository/API name used by existing handlers.
type Session = ClusterSession

type Task struct {
	ID             string
	SchemaVersion  int
	PrincipalID    string
	SessionID      string
	Type           string
	State          remotetask.State
	Spec           json.RawMessage
	Result         json.RawMessage
	IdempotencyKey string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ExpiresAt      *time.Time
}

type ResourceSnapshot struct {
	ID            string
	SchemaVersion int
	TaskID        string
	Kind          string
	Namespace     string
	Name          string
	Data          json.RawMessage
	CreatedAt     time.Time
}

type IdempotencyRecord struct {
	SchemaVersion int
	Scope         string
	Key           string
	RequestHash   string
	ResourceType  string
	ResourceID    string
	Response      json.RawMessage
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

type AuditEvent struct {
	ID            string
	SchemaVersion int
	PrincipalID   string
	Action        string
	ResourceType  string
	ResourceID    string
	Outcome       string
	RequestID     string
	Metadata      json.RawMessage
	CreatedAt     time.Time
}

type AuditFilter struct {
	PrincipalID string
	Action      string
	After       time.Time
	Before      time.Time
	Cursor      *PageCursor
	Limit       int
}

type RelayDesiredState struct {
	RelayID                   string
	SchemaVersion             int
	DesiredState              string
	Version                   uint64
	UpdatedBy                 string
	UpdatedAuthenticationType string
	Reason                    string
	UpdatedAt                 time.Time
}

type AuditExportJob struct {
	ID                          string
	SchemaVersion               int
	State                       string
	Filter                      json.RawMessage
	Result                      string
	ErrorCode                   string
	RequestedBy                 string
	RequestedAuthenticationType string
	Reason                      string
	CreatedAt                   time.Time
	UpdatedAt                   time.Time
	ExpiresAt                   time.Time
}

type AuthAttempt struct {
	ID                   string
	SchemaVersion        int
	ProviderID           string
	StateHash            []byte
	ClientState          string
	ClientCallback       string
	ClientID             string
	Scope                string
	Nonce                string
	PKCEChallenge        string
	UpstreamPKCEVerifier string
	CreatedAt            time.Time
	ExpiresAt            time.Time
}

type AuthExchange struct {
	SchemaVersion int
	CodeHash      []byte
	PrincipalID   string
	ProviderID    string
	ClientID      string
	RedirectURI   string
	Scope         string
	Nonce         string
	PKCEChallenge string
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

type AdminSession struct {
	IDHash               []byte
	SchemaVersion        int
	PrincipalID          string
	TokenFamilyID        string
	AuthenticationType   string
	BreakGlassGeneration string
	CSRFTokenHash        []byte
	CreatedAt            time.Time
	LastSeenAt           time.Time
	IdleExpiresAt        time.Time
	AbsoluteExpiresAt    time.Time
	RevokedAt            *time.Time
}

// LocalAdminUser stores management-plane credentials for a Principal. Password
// and MFA material are always persisted as hashes or authenticated ciphertext.
type LocalAdminUser struct {
	PrincipalID         string
	SchemaVersion       int
	Username            string
	PasswordHash        string
	Enabled             bool
	TOTPSecretEncrypted []byte
	BootstrapComplete   bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type AdminPolicyRevision struct {
	Revision                  uint64
	ID                        string
	SchemaVersion             int
	Spec                      json.RawMessage
	SpecHash                  string
	ValidationState           string
	Validation                json.RawMessage
	CreatedBy                 string
	CreatedAuthenticationType string
	Reason                    string
	CreatedAt                 time.Time
}

type ProviderConfigRevision struct {
	Revision                  uint64
	ID                        string
	SchemaVersion             int
	ProviderID                string
	ProviderType              string
	Config                    json.RawMessage
	ConfigHash                string
	SecretAliases             json.RawMessage
	ValidationState           string
	Validation                json.RawMessage
	CreatedBy                 string
	CreatedAuthenticationType string
	Reason                    string
	CreatedAt                 time.Time
}

type AdminAssignment struct {
	ID             string
	SchemaVersion  int
	PolicyRevision uint64
	Role           string
	Subjects       json.RawMessage
	Groups         json.RawMessage
	Namespaces     json.RawMessage
	CreatedAt      time.Time
}

type ActiveManagementRevision struct {
	ConfigurationType         string
	ConfigurationID           string
	Revision                  uint64
	ETag                      uint64
	UpdatedBy                 string
	UpdatedAuthenticationType string
	UpdatedAt                 time.Time
}

type ConfigChangeRequest struct {
	ID                          string
	SchemaVersion               int
	ConfigurationType           string
	ConfigurationID             string
	BaseRevision                uint64
	BaseETag                    uint64
	ProposedRevision            uint64
	Status                      string
	IdempotencyHash             []byte
	RequestHash                 string
	RequestedBy                 string
	RequestedAuthenticationType string
	Reason                      string
	Validation                  json.RawMessage
	CreatedAt                   time.Time
	UpdatedAt                   time.Time
}
