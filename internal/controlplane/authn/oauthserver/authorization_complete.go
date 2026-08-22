package oauthserver

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"time"

	"github.com/ory/fosite"

	controlstorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func (endpoints *Endpoints) CompleteAuthorization(
	rw http.ResponseWriter,
	request *http.Request,
	transaction, csrf string,
	identity BrowserIdentity,
	allow bool,
) error {
	stored, err := endpoints.repositories.OAuthAuthorizationRequests().
		Consume(request.Context(), signatureHash(transaction), time.Now().UTC())
	if err != nil ||
		subtle.ConstantTimeCompare(stored.CSRFHash, signatureHash(csrf)) != 1 {
		return fosite.ErrInvalidRequest
	}
	return endpoints.completeStoredAuthorization(
		rw,
		request,
		stored,
		identity,
		allow,
	)
}

func (endpoints *Endpoints) completeStoredAuthorization(
	rw http.ResponseWriter,
	request *http.Request,
	stored controlstorage.OAuthAuthorizationRequest,
	browserIdentity BrowserIdentity,
	allow bool,
) error {
	now := time.Now().UTC()
	if browserIdentity.AuthTime.IsZero() {
		browserIdentity.AuthTime = now
	}
	identity := browserIdentity.Identity
	var dto authorizationRequestDTO
	if json.Unmarshal(stored.RequestJSON, &dto) != nil {
		return fosite.ErrServerError
	}
	original, err := http.NewRequestWithContext(
		request.Context(),
		http.MethodGet,
		dto.URL,
		nil,
	)
	if err != nil {
		return fosite.ErrServerError
	}
	authorizeRequest, err := endpoints.provider.NewAuthorizeRequest(
		request.Context(),
		original,
	)
	if err != nil {
		endpoints.provider.WriteAuthorizeError(
			request.Context(),
			rw,
			authorizeRequest,
			err,
		)
		return err
	}
	client, err := endpoints.repositories.OAuthClients().
		Get(request.Context(), authorizeRequest.GetClient().GetID())
	if err != nil {
		endpoints.provider.WriteAuthorizeError(
			request.Context(),
			rw,
			authorizeRequest,
			fosite.ErrAccessDenied,
		)
		return fosite.ErrAccessDenied
	}
	scopes := append([]string(nil), authorizeRequest.GetRequestedScopes()...)
	scopeHash := exactScopeHash(scopes)
	consented, err := endpoints.repositories.OAuthConsents().
		Has(request.Context(), identity.ID, client.ID, scopeHash)
	if err != nil {
		return err
	}
	if !client.Builtin && !client.Trusted && !consented && !allow {
		endpoints.provider.WriteAuthorizeError(
			request.Context(),
			rw,
			authorizeRequest,
			fosite.ErrAccessDenied,
		)
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
	session.IdentityID, session.ProviderID = identity.ID, providerLocal
	session.DisplayName, session.Email = identity.DisplayName, identity.PrimaryEmail
	session.AuthorizationID = authorizeRequest.GetID()
	session.SetSubject(identity.ID)
	session.IDTokenClaims().Subject = identity.ID
	session.IDTokenClaims().AuthTime = browserIdentity.AuthTime
	session.IDTokenClaims().RequestedAt = authorizeRequest.GetRequestedAt()
	session.IDTokenClaims().AuthenticationMethodsReferences = []string{"pwd"}
	session.IDTokenClaims().Add("name", identity.DisplayName)
	session.IDTokenClaims().Add("email", identity.PrimaryEmail)
	response, err := endpoints.provider.NewAuthorizeResponse(
		request.Context(),
		authorizeRequest,
		session,
	)
	if err != nil {
		endpoints.provider.WriteAuthorizeError(
			request.Context(),
			rw,
			authorizeRequest,
			err,
		)
		return err
	}
	if writeDesktopAuthorizationComplete(
		rw,
		request,
		authorizeRequest,
		response,
	) {
		return nil
	}
	endpoints.provider.WriteAuthorizeResponse(
		request.Context(),
		rw,
		authorizeRequest,
		response,
	)
	return nil
}
