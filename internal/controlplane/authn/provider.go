package authn

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

type ProviderType string

const (
	ProviderOIDC      ProviderType = "oidc"
	ProviderAnonymous ProviderType = "anonymous"
)

type Interaction string

const (
	InteractionBrowser Interaction = "browser"
	InteractionNone    Interaction = "none"
)

type Descriptor struct {
	ID          string       `json:"id"`
	Type        ProviderType `json:"type"`
	DisplayName string       `json:"displayName,omitempty"`
	Interaction Interaction  `json:"interaction"`
}

// Identity is the normalized result of one successful upstream
// authentication. It is not a Gateway token and grants no permissions.
type Identity struct {
	ProviderID string
	Issuer     string
	Subject    string
	// DevelopmentSubject is reserved for explicitly enabled development
	// authentication Providers. Production identities use OIDC.
	DevelopmentSubject string
	DisplayName        string
	Email              string
	Groups             []string
}

func (identity Identity) ExternalID() (string, error) {
	switch {
	case identity.Issuer != "" || identity.Subject != "":
		issuer := strings.TrimSpace(identity.Issuer)
		subject := strings.TrimSpace(identity.Subject)
		if issuer == "" || subject == "" {
			return "", errors.New("OIDC identity requires issuer and subject")
		}
		parsed, err := url.Parse(issuer)
		if err != nil || !parsed.IsAbs() || parsed.Scheme != "https" || parsed.Host == "" {
			return "", errors.New("OIDC identity issuer must be an absolute HTTPS URL")
		}
		return issuer + "\x00" + subject, nil
	case identity.DevelopmentSubject != "":
		subject := strings.TrimSpace(identity.DevelopmentSubject)
		if subject == "" || len(subject) > 256 {
			return "", errors.New("development identity subject is invalid")
		}
		return subject, nil
	default:
		return "", errors.New("identity has no stable external ID")
	}
}

// Provider validates upstream credentials and returns a normalized Identity.
// Token issuance, authorization, persistence and sessions live outside this
// interface so they cannot depend on a concrete OIDC SDK.
type Provider interface {
	Descriptor() Descriptor
	Check(context.Context) error
}

type BrowserLoginRequest struct {
	ClientCallback string
	PKCEChallenge  string
	State          string
	Nonce          string
}

type BrowserLogin struct {
	AuthorizationURL string
	TransactionID    string
	ExpiresAt        time.Time
}

type BrowserCallback struct {
	TransactionID string
	Code          string
	State         string
	Issuer        string
}

type BrowserProvider interface {
	Provider
	BeginBrowserLogin(context.Context, BrowserLoginRequest) (BrowserLogin, error)
	CompleteBrowserLogin(context.Context, BrowserCallback) (Identity, error)
}

// AuthorizationCodeProvider is the Gateway-side OIDC capability. The login
// service owns state, callback and exchange-code persistence; the Provider
// owns only upstream authorization URL creation and code/ID-token validation.
type AuthorizationCodeProvider interface {
	Provider
	AuthorizationURL(state, nonce, pkceChallenge string) (string, error)
	Exchange(context.Context, string, string, string) (Identity, error)
}

type AnonymousProvider interface {
	Provider
	AuthenticateAnonymous(context.Context) (Identity, error)
}
