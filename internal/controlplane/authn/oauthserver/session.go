package oauthserver

import (
	"encoding/json"

	"github.com/ory/fosite"
	"github.com/ory/fosite/handler/openid"
)

// Session is the stable Fosite session payload persisted by KubeLoop. It keeps
// authorization attributes next to the protocol claims so opaque tokens can be
// introspected without a second identity lookup.
type Session struct {
	*openid.DefaultSession
	IdentityID      string   `json:"identity_id"`
	ProviderID      string   `json:"provider_id"`
	Groups          []string `json:"groups,omitempty"`
	DisplayName     string   `json:"display_name,omitempty"`
	Email           string   `json:"email,omitempty"`
	AuthorizationID string   `json:"authorization_id"`
	Machine         bool     `json:"machine,omitempty"`
	DeviceID        string   `json:"device_id,omitempty"`
}

func NewSession() *Session { return &Session{DefaultSession: openid.NewDefaultSession()} }

func (session *Session) Clone() fosite.Session {
	if session == nil {
		return nil
	}
	raw, _ := json.Marshal(session)
	clone := NewSession()
	_ = json.Unmarshal(raw, clone)
	return clone
}
