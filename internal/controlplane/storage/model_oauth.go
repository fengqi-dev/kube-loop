package storage

import (
	"encoding/json"
	"time"
)

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
