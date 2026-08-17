package oauthserver

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
	controlstorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/go-jose/go-jose/v4"
	"github.com/ory/fosite"
)

type Endpoints struct {
	provider     fosite.OAuth2Provider
	repositories controlstorage.Repositories
	keyID        string
	signingKey   *ecdsa.PrivateKey
	localAuth    LocalAuthenticator
}

type LocalAuthenticator func(context.Context, string, []byte, string) (controlstorage.Identity, error)

const (
	BrowserSessionCookie     = "__Host-kubeloop-sso"
	HTTPBrowserSessionCookie = "kubeloop-sso"
	browserSessionLifetime   = 12 * time.Hour
)

func (endpoints *Endpoints) SetLocalAuthenticator(authenticator LocalAuthenticator) {
	endpoints.localAuth = authenticator
}

func NewEndpoints(provider fosite.OAuth2Provider, repositories controlstorage.Repositories, keyID string, signingKey *ecdsa.PrivateKey) (*Endpoints, error) {
	if provider == nil || repositories == nil || signingKey == nil || strings.TrimSpace(keyID) == "" {
		return nil, errors.New("Fosite provider and repositories are required")
	}
	return &Endpoints{provider: provider, repositories: repositories, keyID: strings.TrimSpace(keyID), signingKey: signingKey}, nil
}

func (endpoints *Endpoints) KeySet() jose.JSONWebKeySet {
	return jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &endpoints.signingKey.PublicKey, KeyID: endpoints.keyID, Algorithm: string(jose.ES256), Use: "sig"}}}
}

func (endpoints *Endpoints) Token(rw http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	normalizeClientSecretPost(request)
	session := NewSession()
	session.DeviceID = strings.TrimSpace(request.FormValue("device_id"))
	accessRequest, err := endpoints.provider.NewAccessRequest(ctx, request, session)
	if err != nil {
		endpoints.provider.WriteAccessError(ctx, rw, accessRequest, err)
		return
	}
	// Authorization-code and refresh grants restore the persisted session into
	// the access request. Continue mutating that instance so device metadata is
	// persisted on the newly issued opaque token instead of being lost on the
	// throw-away session passed to NewAccessRequest.
	if persisted, ok := accessRequest.GetSession().(*Session); ok {
		session = persisted
		if device := strings.TrimSpace(request.FormValue("device_id")); device != "" {
			session.DeviceID = device
		}
	}
	client, err := endpoints.repositories.OAuthClients().Get(ctx, accessRequest.GetClient().GetID())
	if err != nil {
		endpoints.provider.WriteAccessError(ctx, rw, accessRequest, fosite.ErrInvalidClient)
		return
	}
	grant := accessRequest.GetGrantTypes()
	if grant.Has("client_credentials") {
		if client.Public || client.MachineIdentityID == "" || containsIdentityScope(accessRequest.GetRequestedScopes()) {
			endpoints.provider.WriteAccessError(ctx, rw, accessRequest, fosite.ErrUnauthorizedClient)
			return
		}
		if err := endpoints.enrichIdentity(ctx, session, client.MachineIdentityID); err != nil {
			endpoints.provider.WriteAccessError(ctx, rw, accessRequest, fosite.ErrInvalidClient)
			return
		}
		session.Machine = true
		session.ProviderID = "client_credentials"
		session.SetSubject(client.MachineIdentityID)
	}
	if grant.Has("client_credentials") {
		for _, scope := range accessRequest.GetRequestedScopes() {
			accessRequest.GrantScope(scope)
		}
	}
	response, err := endpoints.provider.NewAccessResponse(ctx, accessRequest)
	if err != nil {
		endpoints.provider.WriteAccessError(ctx, rw, accessRequest, err)
		return
	}
	endpoints.provider.WriteAccessResponse(ctx, rw, accessRequest, response)
}

// Authenticate implements the management token exchange interface while the
// public access token remains opaque.
func (endpoints *Endpoints) Authenticate(ctx context.Context, token string) (authn.AccessIdentity, error) {
	session, requester, err := endpoints.IntrospectAccessToken(ctx, token)
	if err != nil {
		return authn.AccessIdentity{}, err
	}
	identity, err := endpoints.repositories.Identities().GetByID(ctx, session.IdentityID)
	if err != nil {
		return authn.AccessIdentity{}, err
	}
	return authn.AccessIdentity{Identity: identity, ProviderID: session.ProviderID,
		AuthorizationID: session.AuthorizationID, DeviceID: session.DeviceID,
		TokenID: requester.GetID(), AccessExpiresAt: requester.GetSession().GetExpiresAt(fosite.AccessToken)}, nil
}

// Fosite models one token_endpoint_auth_method per client. KubeLoop explicitly
// supports both RFC 6749 basic and post for confidential clients, so post is
// normalized to the equivalent Basic credentials before Fosite authenticates
// the request. The plaintext secret is removed from the form immediately.
func normalizeClientSecretPost(request *http.Request) {
	if _, _, ok := request.BasicAuth(); ok {
		return
	}
	if err := request.ParseForm(); err != nil {
		return
	}
	clientID, secret := request.PostForm.Get("client_id"), request.PostForm.Get("client_secret")
	if clientID == "" || secret == "" {
		return
	}
	request.SetBasicAuth(clientID, secret)
	request.PostForm.Del("client_secret")
	request.Form.Del("client_secret")
	encoded := request.PostForm.Encode()
	request.Body = io.NopCloser(strings.NewReader(encoded))
	request.ContentLength = int64(len(encoded))
}

func (endpoints *Endpoints) Revoke(rw http.ResponseWriter, request *http.Request) {
	err := endpoints.provider.NewRevocationRequest(request.Context(), request)
	endpoints.provider.WriteRevocationResponse(request.Context(), rw, err)
}

func (endpoints *Endpoints) IntrospectAccessToken(ctx context.Context, token string) (*Session, fosite.AccessRequester, error) {
	session := NewSession()
	use, requester, err := endpoints.provider.IntrospectToken(ctx, token, fosite.AccessToken, session)
	if err != nil || use != fosite.AccessToken {
		return nil, nil, fosite.ErrInactiveToken
	}
	stored, ok := requester.GetSession().(*Session)
	if !ok || stored.IdentityID == "" {
		return nil, nil, fosite.ErrInactiveToken
	}
	return stored, requester, nil
}

func (endpoints *Endpoints) enrichIdentity(ctx context.Context, session *Session, id string) error {
	identity, err := endpoints.repositories.Identities().GetByID(ctx, id)
	if err != nil {
		return err
	}
	if identity.Status != "active" {
		return controlstorage.ErrNotFound
	}
	session.IdentityID = identity.ID
	session.DisplayName = identity.DisplayName
	session.Email = identity.PrimaryEmail
	session.SetSubject(identity.ID)
	return nil
}

func containsIdentityScope(scopes fosite.Arguments) bool {
	for _, scope := range []string{"openid", "profile", "email", "offline_access"} {
		if slices.Contains(scopes, scope) {
			return true
		}
	}
	return false
}

func BearerToken(request *http.Request) string {
	authorization := strings.TrimSpace(request.Header.Get("Authorization"))
	if len(authorization) < 8 || !strings.EqualFold(authorization[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(authorization[7:])
}
