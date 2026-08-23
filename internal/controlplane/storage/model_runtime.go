package storage

import (
	"encoding/json"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

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
