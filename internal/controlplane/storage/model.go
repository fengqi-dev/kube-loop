package storage

import (
	"encoding/json"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

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
	ID        string
	TaskID    string
	Kind      string
	Namespace string
	Name      string
	Data      json.RawMessage
	CreatedAt time.Time
}

type IdempotencyRecord struct {
	Scope        string
	Key          string
	RequestHash  string
	ResourceType string
	ResourceID   string
	Response     json.RawMessage
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

type AuditEvent struct {
	ID           string
	IdentityID   string
	Action       string
	ResourceType string
	ResourceID   string
	Outcome      string
	RequestID    string
	Metadata     json.RawMessage
	CreatedAt    time.Time
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
	DesiredState              string
	Version                   uint64
	UpdatedBy                 string
	UpdatedAuthenticationType string
	Reason                    string
	UpdatedAt                 time.Time
}

type AdminSession struct {
	IDHash             []byte
	IdentityID         string
	AuthorizationID    string
	AuthenticationType string
	CSRFTokenHash      []byte
	AuthenticatedAt    time.Time
	CreatedAt          time.Time
	LastSeenAt         time.Time
	IdleExpiresAt      time.Time
	AbsoluteExpiresAt  time.Time
	RevokedAt          *time.Time
}

// OAuthClient is the durable, administrator-managed OAuth 2.0 client record.
// Secret material is deliberately stored separately so reads cannot accidentally
// disclose even the password hash through ordinary client APIs.
type OAuthClient struct {
	ID                string
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
