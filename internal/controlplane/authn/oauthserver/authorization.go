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
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
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
	Principal controlstorage.Principal
	AuthTime  time.Time
}

func (endpoints *Endpoints) ConsentRequired(ctx context.Context, challenge AuthorizationChallenge, principalID string) (bool, error) {
	if challenge.Trusted {
		return false, nil
	}
	has, err := endpoints.repositories.OAuthConsents().Has(ctx, principalID, challenge.Client.ID, exactScopeHash(challenge.Scopes))
	return !has, err
}

type authorizationRequestDTO struct {
	URL              string `json:"url"`
	UpstreamState    string `json:"upstream_state,omitempty"`
	UpstreamNonce    string `json:"upstream_nonce,omitempty"`
	UpstreamVerifier string `json:"upstream_verifier,omitempty"`
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
	err = endpoints.repositories.OAuthAuthorizationRequests().Create(ctx, controlstorage.OAuthAuthorizationRequest{
		ChallengeHash: signatureHash(transaction), RequestID: uuid.NewString(), RequestJSON: raw,
		CSRFHash: signatureHash(csrf), ProviderID: request.URL.Query().Get("provider"), Status: "pending",
		CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	})
	if err != nil {
		return AuthorizationChallenge{}, err
	}
	return AuthorizationChallenge{Transaction: transaction, CSRF: csrf, Client: client,
		Scopes: append([]string(nil), authorizeRequest.GetRequestedScopes()...), Trusted: client.Builtin || client.Trusted}, nil
}

func (endpoints *Endpoints) AuthenticateLocal(ctx context.Context, username string, password []byte, secondFactor, requestID string) (controlstorage.Principal, error) {
	if endpoints == nil || endpoints.repositories == nil {
		return controlstorage.Principal{}, fosite.ErrServerError
	}
	storage, ok := endpoints.providerStorage()
	if !ok || storage.passwordAuthenticator == nil {
		return controlstorage.Principal{}, fosite.ErrNotFound
	}
	return storage.passwordAuthenticator(ctx, username, password, secondFactor, requestID, "browser", "")
}

func (endpoints *Endpoints) CreateBrowserSession(ctx context.Context, principal controlstorage.Principal, ttl time.Duration) (string, error) {
	if principal.ID == "" || principal.Provider == "" || ttl <= 0 {
		return "", fosite.ErrServerError
	}
	token, err := randomAuthorizationValue()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	err = endpoints.repositories.OAuthBrowserSessions().Create(ctx, controlstorage.OAuthBrowserSession{
		IDHash: signatureHash(token), PrincipalID: principal.ID, ProviderID: principal.Provider,
		AuthTime: now, CreatedAt: now, ExpiresAt: now.Add(ttl),
	})
	return token, err
}

func (endpoints *Endpoints) BrowserIdentity(ctx context.Context, token string) (BrowserIdentity, error) {
	stored, err := endpoints.repositories.OAuthBrowserSessions().Get(ctx, signatureHash(token), time.Now().UTC())
	if err != nil {
		return BrowserIdentity{}, err
	}
	principal, err := endpoints.repositories.Principals().GetByID(ctx, stored.PrincipalID)
	if err != nil || principal.Provider != stored.ProviderID {
		return BrowserIdentity{}, fosite.ErrNotFound
	}
	return BrowserIdentity{Principal: principal, AuthTime: stored.AuthTime}, nil
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

func (endpoints *Endpoints) BeginUpstreamAuthorization(ctx context.Context, transaction, csrf, providerID string) (string, error) {
	stored, err := endpoints.repositories.OAuthAuthorizationRequests().Get(ctx, signatureHash(transaction), time.Now().UTC())
	if err != nil || subtle.ConstantTimeCompare(stored.CSRFHash, signatureHash(csrf)) != 1 || endpoints.registry == nil {
		return "", fosite.ErrInvalidRequest
	}
	provider, ok := endpoints.registry.Provider(strings.TrimSpace(providerID))
	if !ok {
		return "", fosite.ErrInvalidRequest
	}
	browser, ok := provider.(authn.AuthorizationCodeProvider)
	if !ok {
		return "", fosite.ErrInvalidRequest
	}
	state, err := randomAuthorizationValue()
	if err != nil {
		return "", err
	}
	nonce, err := randomAuthorizationValue()
	if err != nil {
		return "", err
	}
	verifier, err := randomAuthorizationValue()
	if err != nil {
		return "", err
	}
	challenge := signatureHash(verifier)
	authorizationURL, err := browser.AuthorizationURL(state, nonce, base64.RawURLEncoding.EncodeToString(challenge))
	if err != nil {
		return "", err
	}
	var dto authorizationRequestDTO
	if json.Unmarshal(stored.RequestJSON, &dto) != nil {
		return "", fosite.ErrServerError
	}
	dto.UpstreamState, dto.UpstreamNonce, dto.UpstreamVerifier = state, nonce, verifier
	raw, err := json.Marshal(dto)
	if err != nil {
		return "", fosite.ErrServerError
	}
	if err := endpoints.repositories.OAuthAuthorizationRequests().SetUpstream(ctx, stored.ChallengeHash, signatureHash(state), raw, providerID, time.Now().UTC()); err != nil {
		return "", fosite.ErrServerError
	}
	return authorizationURL, nil
}

func (endpoints *Endpoints) CompleteUpstreamAuthorization(rw http.ResponseWriter, request *http.Request, providerID, code, state string) error {
	stored, err := endpoints.repositories.OAuthAuthorizationRequests().ConsumeUpstream(request.Context(), signatureHash(state), time.Now().UTC())
	if err != nil || stored.ProviderID != providerID || endpoints.registry == nil {
		return fosite.ErrInvalidRequest
	}
	provider, ok := endpoints.registry.Provider(providerID)
	if !ok {
		return fosite.ErrInvalidRequest
	}
	browser, ok := provider.(authn.AuthorizationCodeProvider)
	if !ok {
		return fosite.ErrInvalidRequest
	}
	var dto authorizationRequestDTO
	if json.Unmarshal(stored.RequestJSON, &dto) != nil || subtle.ConstantTimeCompare([]byte(dto.UpstreamState), []byte(state)) != 1 {
		return fosite.ErrInvalidRequest
	}
	identity, err := browser.Exchange(request.Context(), code, dto.UpstreamVerifier, dto.UpstreamNonce)
	if err != nil || identity.ProviderID != providerID {
		return fosite.ErrAccessDenied
	}
	externalID, err := identity.ExternalID()
	if err != nil {
		return fosite.ErrAccessDenied
	}
	now := time.Now().UTC()
	principal, err := endpoints.repositories.Principals().Upsert(request.Context(), controlstorage.Principal{
		ID: uuid.NewString(), Provider: identity.ProviderID, ExternalID: externalID, DisplayName: identity.DisplayName,
		Email: identity.Email, Groups: identity.Groups, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return fosite.ErrServerError
	}
	identityToken, err := endpoints.CreateBrowserSession(request.Context(), principal, browserSessionLifetime)
	if err != nil {
		return fosite.ErrServerError
	}
	http.SetCookie(rw, &http.Cookie{Name: BrowserSessionCookie, Value: identityToken, Path: "/", Secure: true,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(browserSessionLifetime.Seconds())})
	var originalDTO authorizationRequestDTO
	if json.Unmarshal(stored.RequestJSON, &originalDTO) != nil {
		return fosite.ErrServerError
	}
	original, err := http.NewRequestWithContext(request.Context(), http.MethodGet, originalDTO.URL, nil)
	if err != nil {
		return fosite.ErrServerError
	}
	authorizeRequest, err := endpoints.provider.NewAuthorizeRequest(request.Context(), original)
	if err != nil {
		return err
	}
	client, err := endpoints.repositories.OAuthClients().Get(request.Context(), authorizeRequest.GetClient().GetID())
	if err != nil {
		return fosite.ErrInvalidClient
	}
	scopes := append([]string(nil), authorizeRequest.GetRequestedScopes()...)
	consented, err := endpoints.repositories.OAuthConsents().Has(request.Context(), principal.ID, client.ID, exactScopeHash(scopes))
	if err != nil {
		return fosite.ErrServerError
	}
	if !client.Builtin && !client.Trusted && !consented {
		transaction, randomErr := randomAuthorizationValue()
		if randomErr != nil {
			return fosite.ErrServerError
		}
		csrf, randomErr := randomAuthorizationValue()
		if randomErr != nil {
			return fosite.ErrServerError
		}
		if err := endpoints.repositories.OAuthAuthorizationRequests().Continue(request.Context(), stored.ChallengeHash,
			signatureHash(transaction), signatureHash(csrf), principal.ID, now); err != nil {
			return fosite.ErrServerError
		}
		query := url.Values{"transaction": {transaction}, "csrf": {csrf}, "client": {client.Name}, "session": {"true"}, "consent": {"true"}, "scope": {strings.Join(scopes, " ")}}
		http.Redirect(rw, request, "/oauth2/ui/?"+query.Encode(), http.StatusSeeOther)
		return nil
	}
	consumed, err := endpoints.repositories.OAuthAuthorizationRequests().Consume(request.Context(), stored.ChallengeHash, now)
	if err != nil {
		return fosite.ErrInvalidRequest
	}
	return endpoints.completeStoredAuthorization(rw, request, consumed, principal, false, now)
}

// providerStorage is deliberately supplied through the Endpoints constructor;
// it avoids relying on Fosite implementation internals.
func (endpoints *Endpoints) providerStorage() (*Storage, bool) {
	return endpoints.storage, endpoints.storage != nil
}

func (endpoints *Endpoints) CompleteAuthorization(rw http.ResponseWriter, request *http.Request, transaction, csrf string, principal controlstorage.Principal, allow bool, authTimes ...time.Time) error {
	now := time.Now().UTC()
	stored, err := endpoints.repositories.OAuthAuthorizationRequests().Consume(request.Context(), signatureHash(transaction), now)
	if err != nil {
		return fosite.ErrInvalidRequest
	}
	if subtle.ConstantTimeCompare(stored.CSRFHash, signatureHash(csrf)) != 1 {
		return fosite.ErrInvalidRequest
	}
	authTime := time.Time{}
	if len(authTimes) > 0 {
		authTime = authTimes[0]
	}
	return endpoints.completeStoredAuthorization(rw, request, stored, principal, allow, authTime)
}

func (endpoints *Endpoints) completeStoredAuthorization(rw http.ResponseWriter, request *http.Request, stored controlstorage.OAuthAuthorizationRequest, principal controlstorage.Principal, allow bool, authTime time.Time) error {
	now := time.Now().UTC()
	if authTime.IsZero() {
		authTime = now
	}
	var dto authorizationRequestDTO
	if err := json.Unmarshal(stored.RequestJSON, &dto); err != nil {
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
	if err != nil {
		endpoints.provider.WriteAuthorizeError(request.Context(), rw, authorizeRequest, fosite.ErrInvalidClient)
		return err
	}
	scopes := append([]string(nil), authorizeRequest.GetRequestedScopes()...)
	scopeHash := exactScopeHash(scopes)
	consented, err := endpoints.repositories.OAuthConsents().Has(request.Context(), principal.ID, client.ID, scopeHash)
	if err != nil {
		endpoints.provider.WriteAuthorizeError(request.Context(), rw, authorizeRequest, fosite.ErrServerError)
		return err
	}
	if !client.Builtin && !client.Trusted && !consented && !allow {
		endpoints.provider.WriteAuthorizeError(request.Context(), rw, authorizeRequest, fosite.ErrAccessDenied)
		return fosite.ErrAccessDenied
	}
	if allow && !client.Builtin && !client.Trusted && !consented {
		if err := endpoints.repositories.OAuthConsents().Grant(request.Context(), controlstorage.OAuthConsent{PrincipalID: principal.ID, ClientID: client.ID, ScopeHash: scopeHash, Scopes: scopes, CreatedAt: now, UpdatedAt: now}); err != nil {
			endpoints.provider.WriteAuthorizeError(request.Context(), rw, authorizeRequest, fosite.ErrServerError)
			return err
		}
	}
	for _, scope := range scopes {
		authorizeRequest.GrantScope(scope)
	}
	session := NewSession()
	session.PrincipalID = principal.ID
	session.ProviderID = principal.Provider
	session.DisplayName = principal.DisplayName
	session.Email = principal.Email
	session.Groups = append([]string(nil), principal.Groups...)
	session.AuthorizationID = authorizeRequest.GetID()
	session.SetSubject(principal.ID)
	session.IDTokenClaims().Subject = principal.ID
	session.IDTokenClaims().AuthTime = authTime
	session.IDTokenClaims().RequestedAt = authorizeRequest.GetRequestedAt()
	session.IDTokenClaims().AuthenticationMethodsReferences = []string{principal.Provider}
	session.IDTokenClaims().Add("name", principal.DisplayName)
	session.IDTokenClaims().Add("email", principal.Email)
	session.IDTokenClaims().Add("groups", principal.Groups)
	response, err := endpoints.provider.NewAuthorizeResponse(request.Context(), authorizeRequest, session)
	if err != nil {
		endpoints.provider.WriteAuthorizeError(request.Context(), rw, authorizeRequest, err)
		return err
	}
	endpoints.provider.WriteAuthorizeResponse(request.Context(), rw, authorizeRequest, response)
	return nil
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
