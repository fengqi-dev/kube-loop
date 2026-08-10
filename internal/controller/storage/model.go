package storage

import (
	"encoding/json"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/remotetask"
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
	Limit       int
}

type AuthAttempt struct {
	ID                   string
	SchemaVersion        int
	ProviderID           string
	StateHash            []byte
	ClientState          string
	ClientCallback       string
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
	PKCEChallenge string
	CreatedAt     time.Time
	ExpiresAt     time.Time
}
