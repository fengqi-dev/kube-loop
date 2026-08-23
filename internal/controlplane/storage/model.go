package storage

import (
	"encoding/json"
	"time"
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
