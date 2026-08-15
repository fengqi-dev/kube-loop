package oauthserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	controlstorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
	"github.com/ory/fosite"
)

type AuthorizationChallenge struct {
	Transaction string
	CSRF        string
	Client      controlstorage.OAuthClient
	Scopes      []string
	Trusted     bool
}

type BrowserIdentity struct {
	Identity   controlstorage.Identity
	ProviderID string
	Groups     []string
	AuthTime   time.Time
}

type authorizationRequestDTO struct {
	URL string `json:"url"`
}

func (endpoints *Endpoints) ConsentRequired(ctx context.Context, challenge AuthorizationChallenge, identityID string) (bool, error) {
	if challenge.Trusted {
		return false, nil
	}
	has, err := endpoints.repositories.OAuthConsents().Has(ctx, identityID, challenge.Client.ID, exactScopeHash(challenge.Scopes))
	return !has, err
}

func (endpoints *Endpoints) BeginAuthorization(ctx context.Context, request *http.Request) (AuthorizationChallenge, error) {
	authorizeRequest, err := endpoints.provider.NewAuthorizeRequest(ctx, request)
	if err != nil {
		return AuthorizationChallenge{}, err
	}
	if authorizeRequest.GetResponseTypes().Has("code") &&
		(request.URL.Query().Get("code_challenge_method") != "S256" || request.URL.Query().Get("code_challenge") == "") {
		return AuthorizationChallenge{}, fosite.ErrInvalidRequest.WithHint("Authorization code requests require PKCE S256.")
	}
	client, err := endpoints.repositories.OAuthClients().Get(ctx, authorizeRequest.GetClient().GetID())
	if err != nil {
		return AuthorizationChallenge{}, fosite.ErrInvalidClient
	}
	transaction, err := randomAuthorizationValue()
	if err != nil {
		return AuthorizationChallenge{}, err
	}
	csrf, err := randomAuthorizationValue()
	if err != nil {
		return AuthorizationChallenge{}, err
	}
	raw, err := json.Marshal(authorizationRequestDTO{URL: request.URL.String()})
	if err != nil {
		return AuthorizationChallenge{}, errors.New("encode OAuth authorization request")
	}
	now := time.Now().UTC()
	if err := endpoints.repositories.OAuthAuthorizationRequests().Create(ctx, controlstorage.OAuthAuthorizationRequest{
		ChallengeHash: signatureHash(transaction), RequestID: uuid.NewString(), RequestJSON: raw,
		CSRFHash: signatureHash(csrf), ProviderID: "local", Status: "pending",
		CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		return AuthorizationChallenge{}, err
	}
	return AuthorizationChallenge{Transaction: transaction, CSRF: csrf, Client: client,
		Scopes: append([]string(nil), authorizeRequest.GetRequestedScopes()...), Trusted: client.Builtin || client.Trusted}, nil
}

func (endpoints *Endpoints) AuthenticateLocal(ctx context.Context, username string, password []byte, requestID string) (BrowserIdentity, error) {
	if endpoints == nil || endpoints.repositories == nil || endpoints.localAuth == nil {
		return BrowserIdentity{}, fosite.ErrServerError
	}
	identity, err := endpoints.localAuth(ctx, username, password, requestID)
	if err != nil {
		return BrowserIdentity{}, err
	}
	groups, err := endpoints.identityGroups(ctx, identity.ID)
	if err != nil {
		return BrowserIdentity{}, err
	}
	return BrowserIdentity{Identity: identity, ProviderID: "local", Groups: groups, AuthTime: time.Now().UTC()}, nil
}

func (endpoints *Endpoints) CreateBrowserSession(ctx context.Context, identity BrowserIdentity, ttl time.Duration) (string, error) {
	if identity.Identity.ID == "" || identity.ProviderID != "local" || ttl <= 0 {
		return "", fosite.ErrServerError
	}
	token, err := randomAuthorizationValue()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	err = endpoints.repositories.OAuthBrowserSessions().Create(ctx, controlstorage.OAuthBrowserSession{
		IDHash: signatureHash(token), IdentityID: identity.Identity.ID, ProviderID: "local",
		AuthTime: now, CreatedAt: now, ExpiresAt: now.Add(ttl),
	})
	return token, err
}

func (endpoints *Endpoints) BrowserIdentity(ctx context.Context, token string) (BrowserIdentity, error) {
	stored, err := endpoints.repositories.OAuthBrowserSessions().Get(ctx, signatureHash(token), time.Now().UTC())
	if err != nil {
		return BrowserIdentity{}, err
	}
	if stored.ProviderID != "local" {
		return BrowserIdentity{}, fosite.ErrNotFound
	}
	identity, err := endpoints.repositories.Identities().GetByID(ctx, stored.IdentityID)
	if err != nil || identity.Status != "active" {
		return BrowserIdentity{}, fosite.ErrNotFound
	}
	groups, err := endpoints.identityGroups(ctx, identity.ID)
	if err != nil {
		return BrowserIdentity{}, fosite.ErrNotFound
	}
	return BrowserIdentity{Identity: identity, ProviderID: "local", Groups: groups, AuthTime: stored.AuthTime}, nil
}

func (endpoints *Endpoints) RevokeBrowserSession(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	err := endpoints.repositories.OAuthBrowserSessions().Revoke(ctx, signatureHash(token), time.Now().UTC())
	if errors.Is(err, controlstorage.ErrNotFound) {
		return nil
	}
	return err
}

func (endpoints *Endpoints) CompleteAuthorization(rw http.ResponseWriter, request *http.Request, transaction, csrf string, identity BrowserIdentity, allow bool) error {
	stored, err := endpoints.repositories.OAuthAuthorizationRequests().Consume(request.Context(), signatureHash(transaction), time.Now().UTC())
	if err != nil || subtle.ConstantTimeCompare(stored.CSRFHash, signatureHash(csrf)) != 1 {
		return fosite.ErrInvalidRequest
	}
	return endpoints.completeStoredAuthorization(rw, request, stored, identity, allow)
}

func (endpoints *Endpoints) completeStoredAuthorization(rw http.ResponseWriter, request *http.Request, stored controlstorage.OAuthAuthorizationRequest, browserIdentity BrowserIdentity, allow bool) error {
	now := time.Now().UTC()
	if browserIdentity.AuthTime.IsZero() {
		browserIdentity.AuthTime = now
	}
	identity := browserIdentity.Identity
	var dto authorizationRequestDTO
	if json.Unmarshal(stored.RequestJSON, &dto) != nil {
		return fosite.ErrServerError
	}
	original, err := http.NewRequestWithContext(request.Context(), http.MethodGet, dto.URL, nil)
	if err != nil {
		return fosite.ErrServerError
	}
	authorizeRequest, err := endpoints.provider.NewAuthorizeRequest(request.Context(), original)
	if err != nil {
		endpoints.provider.WriteAuthorizeError(request.Context(), rw, authorizeRequest, err)
		return err
	}
	client, err := endpoints.repositories.OAuthClients().Get(request.Context(), authorizeRequest.GetClient().GetID())
	if err != nil || !endpoints.identityAllowedForClient(request.Context(), identity.ID, client) {
		endpoints.provider.WriteAuthorizeError(request.Context(), rw, authorizeRequest, fosite.ErrAccessDenied)
		return fosite.ErrAccessDenied
	}
	scopes := append([]string(nil), authorizeRequest.GetRequestedScopes()...)
	scopeHash := exactScopeHash(scopes)
	consented, err := endpoints.repositories.OAuthConsents().Has(request.Context(), identity.ID, client.ID, scopeHash)
	if err != nil {
		return err
	}
	if !client.Builtin && !client.Trusted && !consented && !allow {
		endpoints.provider.WriteAuthorizeError(request.Context(), rw, authorizeRequest, fosite.ErrAccessDenied)
		return fosite.ErrAccessDenied
	}
	if allow && !client.Builtin && !client.Trusted && !consented {
		if err := endpoints.repositories.OAuthConsents().Grant(request.Context(), controlstorage.OAuthConsent{
			IdentityID: identity.ID, ClientID: client.ID, ScopeHash: scopeHash, Scopes: scopes,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
	}
	for _, scope := range scopes {
		authorizeRequest.GrantScope(scope)
	}
	session := NewSession()
	session.IdentityID, session.ProviderID = identity.ID, "local"
	session.DisplayName, session.Email = identity.DisplayName, identity.PrimaryEmail
	session.Groups = append([]string(nil), browserIdentity.Groups...)
	session.AuthorizationID = authorizeRequest.GetID()
	session.SetSubject(identity.ID)
	session.IDTokenClaims().Subject = identity.ID
	session.IDTokenClaims().AuthTime = browserIdentity.AuthTime
	session.IDTokenClaims().RequestedAt = authorizeRequest.GetRequestedAt()
	session.IDTokenClaims().AuthenticationMethodsReferences = []string{"pwd"}
	session.IDTokenClaims().Add("name", identity.DisplayName)
	session.IDTokenClaims().Add("email", identity.PrimaryEmail)
	session.IDTokenClaims().Add("groups", browserIdentity.Groups)
	response, err := endpoints.provider.NewAuthorizeResponse(request.Context(), authorizeRequest, session)
	if err != nil {
		endpoints.provider.WriteAuthorizeError(request.Context(), rw, authorizeRequest, err)
		return err
	}
	endpoints.provider.WriteAuthorizeResponse(request.Context(), rw, authorizeRequest, response)
	return nil
}

func (endpoints *Endpoints) identityAllowedForClient(ctx context.Context, identityID string, client controlstorage.OAuthClient) bool {
	if client.OrganizationID == "" {
		return true
	}
	members, err := endpoints.repositories.Organizations().ListMembers(ctx, client.OrganizationID, controlstorage.MaximumManagementPageFetch)
	return err == nil && slices.ContainsFunc(members, func(member controlstorage.OrganizationMembership) bool {
		return member.IdentityID == identityID && member.Status == "active"
	})
}

func (endpoints *Endpoints) CancelAuthorization(rw http.ResponseWriter, request *http.Request, transaction, csrf string) error {
	return endpoints.DenyAuthorization(rw, request, transaction, csrf, "access_denied")
}

func (endpoints *Endpoints) DenyAuthorization(rw http.ResponseWriter, request *http.Request, transaction, csrf, code string) error {
	stored, err := endpoints.repositories.OAuthAuthorizationRequests().Consume(request.Context(), signatureHash(transaction), time.Now().UTC())
	if err != nil || subtle.ConstantTimeCompare(stored.CSRFHash, signatureHash(csrf)) != 1 {
		return fosite.ErrInvalidRequest
	}
	var dto authorizationRequestDTO
	if json.Unmarshal(stored.RequestJSON, &dto) != nil {
		return fosite.ErrInvalidRequest
	}
	original, err := http.NewRequestWithContext(request.Context(), http.MethodGet, dto.URL, nil)
	if err != nil {
		return err
	}
	authorizeRequest, err := endpoints.provider.NewAuthorizeRequest(request.Context(), original)
	if err != nil {
		return err
	}
	denial := fosite.ErrAccessDenied
	if code == "login_required" {
		denial = fosite.ErrLoginRequired
	} else if code == "consent_required" {
		denial = fosite.ErrConsentRequired
	}
	endpoints.provider.WriteAuthorizeError(request.Context(), rw, authorizeRequest, denial)
	return nil
}

func randomAuthorizationValue() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("generate OAuth authorization value")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func exactScopeHash(scopes []string) []byte {
	scopes = append([]string(nil), scopes...)
	slices.Sort(scopes)
	sum := sha256.Sum256([]byte(strings.Join(slices.Compact(scopes), "\x00")))
	return sum[:]
}
