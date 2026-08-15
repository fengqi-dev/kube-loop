package storage

import (
	"encoding/json"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

const ObjectSchemaVersion = 1

type Identity struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	DisplayName  string    `json:"displayName"`
	PrimaryEmail string    `json:"primaryEmail,omitempty"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type PageCursor struct {
	CreatedAt time.Time
	ID        string
}

type IdentityListFilter struct {
	Type   string
	Status string
	Search string
	Cursor *PageCursor
	Limit  int
}

type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type OrganizationMembership struct {
	OrganizationID string    `json:"organizationId"`
	IdentityID     string    `json:"identityId"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type Group struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organizationId"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	System         bool      `json:"system"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type GroupNamespace struct {
	GroupID   string    `json:"groupId"`
	Namespace string    `json:"namespace"`
	CreatedAt time.Time `json:"createdAt"`
}

type GroupMembership struct {
	GroupID    string    `json:"groupId"`
	IdentityID string    `json:"identityId"`
	SourceType string    `json:"sourceType"`
	SourceID   string    `json:"sourceId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

type Invitation struct {
	ID             string     `json:"id"`
	OrganizationID string     `json:"organizationId"`
	Email          string     `json:"email"`
	GroupID        string     `json:"groupId"`
	TokenHash      []byte     `json:"-"`
	Status         string     `json:"status"`
	InvitedBy      string     `json:"invitedBy"`
	CreatedAt      time.Time  `json:"createdAt"`
	ExpiresAt      time.Time  `json:"expiresAt"`
	AcceptedAt     *time.Time `json:"acceptedAt,omitempty"`
}

type BootstrapToken struct {
	TokenHash  []byte
	CreatedAt  time.Time
	ExpiresAt  time.Time
	ConsumedAt *time.Time
}

type PasswordCredential struct {
	IdentityID   string
	Username     string
	PasswordHash string
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type SecurityPolicy struct {
	ScopeType      string
	OrganizationID string
	Spec           json.RawMessage
	Revision       uint64
	UpdatedBy      string
	UpdatedAt      time.Time
}

type SessionListFilter struct {
	IdentityID string
	Namespace  string
	State      string
	Cursor     *PageCursor
	Limit      int
}

type TaskListFilter struct {
	IdentityID string
	SessionID  string
	Namespace  string
	Type       string
	State      remotetask.State
	Cursor     *PageCursor
	Limit      int
}

// ClusterSession owns one identity/device/cluster/namespace runtime scope.
type ClusterSession struct {
	ID              string
	SchemaVersion   int
	IdentityID      string
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
	IdentityID     string
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
	IdentityID    string
	Action        string
	ResourceType  string
	ResourceID    string
	Outcome       string
	RequestID     string
	Metadata      json.RawMessage
	CreatedAt     time.Time
}

type AuditFilter struct {
	IdentityID string
	Action     string
	After      time.Time
	Before     time.Time
	Cursor     *PageCursor
	Limit      int
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
	IdentityID           string
	AuthorizationID      string
	AuthenticationType   string
	BreakGlassGeneration string
	CSRFTokenHash        []byte
	AuthenticatedAt      time.Time
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
	ID                string
	OrganizationID    string
	Name              string
	Public            bool
	RedirectURIs      []string
	GrantTypes        []string
	Scopes            []string
	Trusted           bool
	Enabled           bool
	Builtin           bool
	MachineIdentityID string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type OAuthClientSecret struct {
	ClientID   string
	SecretHash []byte
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type OAuthSession struct {
	Kind          string
	SignatureHash []byte
	RequestID     string
	IdentityID    string
	ClientID      string
	DeviceID      string
	RequestJSON   json.RawMessage
	Status        string
	CreatedAt     time.Time
	ExpiresAt     time.Time
	RevokedAt     *time.Time
}

type OAuthGrant struct {
	RequestID  string
	IdentityID string
	ClientID   string
	DeviceID   string
	Scopes     []string
	Status     string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
}

type OAuthGrantListFilter struct {
	IdentityID string
	ClientID   string
	Status     string
	Cursor     *PageCursor
	Limit      int
	Now        time.Time
}

type OAuthAuthorizationRequest struct {
	ChallengeHash     []byte
	UpstreamStateHash []byte
	RequestID         string
	RequestJSON       json.RawMessage
	CSRFHash          []byte
	IdentityID        string
	ProviderID        string
	Status            string
	CreatedAt         time.Time
	ExpiresAt         time.Time
}

type OAuthConsent struct {
	IdentityID string
	ClientID   string
	ScopeHash  []byte
	Scopes     []string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type OAuthBrowserSession struct {
	IDHash     []byte
	IdentityID string
	ProviderID string
	AuthTime   time.Time
	CreatedAt  time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
}
