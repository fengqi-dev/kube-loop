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

type AdminSession struct {
	IDHash               []byte
	SchemaVersion        int
	PrincipalID          string
	AuthorizationID      string
	AuthenticationType   string
	BreakGlassGeneration string
	CSRFTokenHash        []byte
	CreatedAt            time.Time
	LastSeenAt           time.Time
	IdleExpiresAt        time.Time
	AbsoluteExpiresAt    time.Time
	RevokedAt            *time.Time
}

// OAuthClient is the durable, administrator-managed OAuth 2.0 client record.
// Secret material is deliberately stored separately so reads cannot accidentally
// disclose even the password hash through ordinary client APIs.
type OAuthClient struct {
	ID                 string
	SchemaVersion      int
	Name               string
	Public             bool
	RedirectURIs       []string
	GrantTypes         []string
	ResponseTypes      []string
	Scopes             []string
	Trusted            bool
	Enabled            bool
	Builtin            bool
	MachinePrincipalID string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type OAuthClientSecret struct {
	ClientID      string
	SchemaVersion int
	SecretHash    []byte
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type OAuthSession struct {
	Kind          string
	SignatureHash []byte
	RequestID     string
	PrincipalID   string
	ClientID      string
	DeviceID      string
	RequestJSON   json.RawMessage
	Status        string
	CreatedAt     time.Time
	ExpiresAt     time.Time
	RevokedAt     *time.Time
}

type OAuthAuthorizationRequest struct {
	ChallengeHash     []byte
	UpstreamStateHash []byte
	RequestID         string
	RequestJSON       json.RawMessage
	CSRFHash          []byte
	PrincipalID       string
	ProviderID        string
	Status            string
	CreatedAt         time.Time
	ExpiresAt         time.Time
}

type OAuthConsent struct {
	PrincipalID string
	ClientID    string
	ScopeHash   []byte
	Scopes      []string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type OAuthBrowserSession struct {
	IDHash      []byte
	PrincipalID string
	ProviderID  string
	AuthTime    time.Time
	CreatedAt   time.Time
	ExpiresAt   time.Time
	RevokedAt   *time.Time
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

type AdminPolicyConfig struct {
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

type ProviderConfig struct {
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

type AuthorizationRoleRecord struct {
	PolicyID   string
	ID         string
	Definition json.RawMessage
}

type AuthorizationBindingRecord struct {
	PolicyID       string
	ID             string
	RoleID         string
	SubjectType    string
	PrincipalID    string
	ProviderID     string
	GroupName      string
	ScopeType      string
	NamespaceNames json.RawMessage
	LabelSelectors json.RawMessage
	ManagedBy      string
	CreatedBy      string
	Binding        json.RawMessage
}

type ActiveManagementConfig struct {
	ConfigurationType         string
	ConfigurationID           string
	ObjectID                  string
	UpdatedBy                 string
	UpdatedAuthenticationType string
	UpdatedAt                 time.Time
}

type ConfigChangeRequest struct {
	ID                          string
	SchemaVersion               int
	ConfigurationType           string
	ConfigurationID             string
	BaseObjectID                string
	ProposedObjectID            string
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
