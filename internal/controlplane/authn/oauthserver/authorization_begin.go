package oauthserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/ory/fosite"

	controlstorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
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
	AuthTime   time.Time
}

type authorizationRequestDTO struct {
	URL string `json:"url"`
}

func (endpoints *Endpoints) ConsentRequired(
	ctx context.Context,
	challenge AuthorizationChallenge,
	identityID string,
) (bool, error) {
	if challenge.Trusted {
		return false, nil
	}
	has, err := endpoints.repositories.OAuthConsents().
		Has(ctx, identityID, challenge.Client.ID, exactScopeHash(challenge.Scopes))
	return !has, err
}

func (endpoints *Endpoints) BeginAuthorization(
	ctx context.Context,
	request *http.Request,
) (AuthorizationChallenge, error) {
	authorizeRequest, err := endpoints.provider.NewAuthorizeRequest(
		ctx,
		request,
	)
	if err != nil {
		return AuthorizationChallenge{}, err
	}
	if authorizeRequest.GetResponseTypes().Has(responseTypeCode) &&
		(request.URL.Query().Get("code_challenge_method") != "S256" || request.URL.Query().Get("code_challenge") == "") {
		return AuthorizationChallenge{}, fosite.ErrInvalidRequest.WithHint(
			"Authorization code requests require PKCE S256.",
		)
	}
	client, err := endpoints.repositories.OAuthClients().
		Get(ctx, authorizeRequest.GetClient().GetID())
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
		return AuthorizationChallenge{}, errors.New(
			"encode OAuth authorization request",
		)
	}
	now := time.Now().UTC()
	if err := endpoints.repositories.OAuthAuthorizationRequests().Create(ctx, controlstorage.OAuthAuthorizationRequest{
		ChallengeHash: signatureHash(transaction), RequestID: uuid.NewString(), RequestJSON: raw,
		CSRFHash: signatureHash(csrf), ProviderID: providerLocal, Status: "pending",
		CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		return AuthorizationChallenge{}, err
	}
	return AuthorizationChallenge{
		Transaction: transaction,
		CSRF:        csrf,
		Client:      client,
		Scopes: append(
			[]string(nil),
			authorizeRequest.GetRequestedScopes()...),
		Trusted: client.Builtin || client.Trusted,
	}, nil
}
