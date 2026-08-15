package oauthserver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	controlstorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/ory/fosite"
	"github.com/ory/fosite/handler/oauth2"
	"github.com/ory/fosite/handler/openid"
	"github.com/ory/fosite/handler/pkce"
)

const (
	kindAuthorizationCode = "authorization_code"
	kindPKCE              = "pkce"
	kindOIDC              = "oidc"
	kindAccessToken       = "access_token"
	kindRefreshToken      = "refresh_token"
)

type Storage struct {
	repositories controlstorage.Repositories
}

type transactionContextKey struct{}

type transactionContext struct {
	transaction controlstorage.RepositoryTransaction
}

var (
	_ fosite.Storage                     = (*Storage)(nil)
	_ oauth2.CoreStorage                 = (*Storage)(nil)
	_ oauth2.TokenRevocationStorage      = (*Storage)(nil)
	_ pkce.PKCERequestStorage            = (*Storage)(nil)
	_ openid.OpenIDConnectRequestStorage = (*Storage)(nil)
	_ interface {
		BeginTX(context.Context) (context.Context, error)
		Commit(context.Context) error
		Rollback(context.Context) error
	} = (*Storage)(nil)
)

func NewStorage(repositories controlstorage.Repositories) (*Storage, error) {
	if repositories == nil {
		return nil, errors.New("OAuth repositories are required")
	}
	return &Storage{repositories: repositories}, nil
}

func (storage *Storage) repositoriesFor(ctx context.Context) controlstorage.Repositories {
	if transaction, ok := ctx.Value(transactionContextKey{}).(*transactionContext); ok && transaction.transaction != nil {
		return transaction.transaction.Repositories()
	}
	return storage.repositories
}

func (storage *Storage) BeginTX(ctx context.Context) (context.Context, error) {
	manager, ok := storage.repositories.(controlstorage.ExplicitTransactionManager)
	if !ok {
		return context.WithValue(ctx, transactionContextKey{}, &transactionContext{}), nil
	}
	transaction, err := manager.BeginTransaction(ctx)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, transactionContextKey{}, &transactionContext{transaction: transaction}), nil
}

func (storage *Storage) Commit(ctx context.Context) error {
	transaction, _ := ctx.Value(transactionContextKey{}).(*transactionContext)
	if transaction == nil || transaction.transaction == nil {
		return nil
	}
	return transaction.transaction.Commit()
}

func (storage *Storage) Rollback(ctx context.Context) error {
	transaction, _ := ctx.Value(transactionContextKey{}).(*transactionContext)
	if transaction == nil || transaction.transaction == nil {
		return nil
	}
	return transaction.transaction.Rollback()
}

func (storage *Storage) GetClient(ctx context.Context, id string) (fosite.Client, error) {
	client, err := storage.repositoriesFor(ctx).OAuthClients().Get(ctx, id)
	if errors.Is(err, controlstorage.ErrNotFound) || (err == nil && !client.Enabled) {
		return nil, fosite.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var secret []byte
	if !client.Public {
		stored, secretErr := storage.repositoriesFor(ctx).OAuthClients().GetSecret(ctx, id)
		if errors.Is(secretErr, controlstorage.ErrNotFound) {
			return nil, fosite.ErrNotFound
		}
		if secretErr != nil {
			return nil, secretErr
		}
		secret = stored.SecretHash
	}
	authMethod := "client_secret_basic"
	if client.Public {
		authMethod = "none"
	}
	return &fosite.DefaultOpenIDConnectClient{DefaultClient: &fosite.DefaultClient{
		ID: client.ID, Secret: secret, RedirectURIs: client.RedirectURIs, GrantTypes: client.GrantTypes,
		ResponseTypes: []string{"code"}, Scopes: client.Scopes, Audience: []string{"kubeloop.api"}, Public: client.Public,
	}, TokenEndpointAuthMethod: authMethod}, nil
}

func (*Storage) ClientAssertionJWTValid(context.Context, string) error { return fosite.ErrNotFound }
func (*Storage) SetClientAssertionJWT(context.Context, string, time.Time) error {
	return fosite.ErrNotFound
}

type requestDTO struct {
	ID                string     `json:"id"`
	RequestedAt       time.Time  `json:"requested_at"`
	ClientID          string     `json:"client_id"`
	RequestedScopes   []string   `json:"requested_scopes"`
	GrantedScopes     []string   `json:"granted_scopes"`
	RequestedAudience []string   `json:"requested_audience"`
	GrantedAudience   []string   `json:"granted_audience"`
	Form              url.Values `json:"form"`
	Session           *Session   `json:"session"`
}

func encodeRequester(request fosite.Requester) (json.RawMessage, error) {
	session, ok := request.GetSession().(*Session)
	if !ok {
		return nil, errors.New("OAuth request session has an invalid type")
	}
	return json.Marshal(requestDTO{ID: request.GetID(), RequestedAt: request.GetRequestedAt(), ClientID: request.GetClient().GetID(),
		RequestedScopes: request.GetRequestedScopes(), GrantedScopes: request.GetGrantedScopes(),
		RequestedAudience: request.GetRequestedAudience(), GrantedAudience: request.GetGrantedAudience(),
		Form: request.GetRequestForm(), Session: session})
}

func (storage *Storage) decodeRequester(ctx context.Context, raw json.RawMessage) (fosite.Requester, error) {
	var dto requestDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		return nil, errors.New("decode OAuth request")
	}
	client, err := storage.GetClient(ctx, dto.ClientID)
	if err != nil {
		return nil, err
	}
	request := fosite.NewRequest()
	request.ID = dto.ID
	request.RequestedAt = dto.RequestedAt
	request.Client = client
	request.RequestedScope = dto.RequestedScopes
	request.GrantedScope = dto.GrantedScopes
	request.RequestedAudience = dto.RequestedAudience
	request.GrantedAudience = dto.GrantedAudience
	request.Form = dto.Form
	if request.Form == nil {
		request.Form = url.Values{}
	}
	request.Session = dto.Session
	if request.Session == nil {
		request.Session = NewSession()
	}
	return request, nil
}

func signatureHash(signature string) []byte { sum := sha256.Sum256([]byte(signature)); return sum[:] }

func (storage *Storage) create(ctx context.Context, kind, signature string, request fosite.Requester, tokenType fosite.TokenType) error {
	raw, err := encodeRequester(request)
	if err != nil {
		return err
	}
	expires := request.GetSession().GetExpiresAt(tokenType)
	if expires.IsZero() {
		expires = time.Now().UTC().Add(10 * time.Minute)
	}
	session, _ := request.GetSession().(*Session)
	identityID, deviceID := "", ""
	if session != nil {
		identityID, deviceID = session.IdentityID, session.DeviceID
	}
	return storage.repositoriesFor(ctx).OAuthSessions().Create(ctx, controlstorage.OAuthSession{Kind: kind,
		SignatureHash: signatureHash(signature), RequestID: request.GetID(), IdentityID: identityID,
		ClientID: request.GetClient().GetID(), DeviceID: deviceID, RequestJSON: raw, Status: "active",
		CreatedAt: time.Now().UTC(), ExpiresAt: expires})
}

func (storage *Storage) get(ctx context.Context, kind, signature string, invalidatedError error) (fosite.Requester, error) {
	stored, err := storage.repositoriesFor(ctx).OAuthSessions().Get(ctx, kind, signatureHash(signature))
	if errors.Is(err, controlstorage.ErrNotFound) {
		return nil, fosite.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	request, err := storage.decodeRequester(ctx, stored.RequestJSON)
	if err != nil {
		return nil, err
	}
	if stored.Status != "active" || !stored.ExpiresAt.After(time.Now().UTC()) {
		if invalidatedError != nil {
			return request, invalidatedError
		}
		return nil, fosite.ErrNotFound
	}
	return request, nil
}

func (storage *Storage) CreateAuthorizeCodeSession(ctx context.Context, code string, request fosite.Requester) error {
	return storage.create(ctx, kindAuthorizationCode, code, request, fosite.AuthorizeCode)
}
func (storage *Storage) GetAuthorizeCodeSession(ctx context.Context, code string, _ fosite.Session) (fosite.Requester, error) {
	return storage.get(ctx, kindAuthorizationCode, code, fosite.ErrInvalidatedAuthorizeCode)
}
func (storage *Storage) InvalidateAuthorizeCodeSession(ctx context.Context, code string) error {
	_, err := storage.repositoriesFor(ctx).OAuthSessions().Consume(ctx, kindAuthorizationCode, signatureHash(code), time.Now().UTC())
	if errors.Is(err, controlstorage.ErrNotFound) {
		return fosite.ErrInvalidatedAuthorizeCode
	}
	return err
}
func (storage *Storage) CreateAccessTokenSession(ctx context.Context, signature string, request fosite.Requester) error {
	return storage.create(ctx, kindAccessToken, signature, request, fosite.AccessToken)
}
func (storage *Storage) GetAccessTokenSession(ctx context.Context, signature string, _ fosite.Session) (fosite.Requester, error) {
	return storage.get(ctx, kindAccessToken, signature, nil)
}
func (storage *Storage) DeleteAccessTokenSession(ctx context.Context, signature string) error {
	return storage.repositoriesFor(ctx).OAuthSessions().Delete(ctx, kindAccessToken, signatureHash(signature))
}
func (storage *Storage) CreateRefreshTokenSession(ctx context.Context, signature, _ string, request fosite.Requester) error {
	return storage.create(ctx, kindRefreshToken, signature, request, fosite.RefreshToken)
}
func (storage *Storage) GetRefreshTokenSession(ctx context.Context, signature string, _ fosite.Session) (fosite.Requester, error) {
	return storage.get(ctx, kindRefreshToken, signature, fosite.ErrInactiveToken)
}
func (storage *Storage) DeleteRefreshTokenSession(ctx context.Context, signature string) error {
	return storage.repositoriesFor(ctx).OAuthSessions().Delete(ctx, kindRefreshToken, signatureHash(signature))
}
func (storage *Storage) RotateRefreshToken(ctx context.Context, requestID, signature string) error {
	_, err := storage.repositoriesFor(ctx).OAuthSessions().Consume(ctx, kindRefreshToken, signatureHash(signature), time.Now().UTC())
	if errors.Is(err, controlstorage.ErrNotFound) {
		_ = storage.repositoriesFor(ctx).OAuthSessions().RevokeRequest(ctx, requestID, time.Now().UTC())
		return fosite.ErrInactiveToken
	}
	return err
}
func (storage *Storage) RevokeRefreshToken(ctx context.Context, requestID string) error {
	return storage.repositoriesFor(ctx).OAuthSessions().RevokeRequest(ctx, requestID, time.Now().UTC())
}
func (storage *Storage) RevokeAccessToken(ctx context.Context, requestID string) error {
	return storage.repositoriesFor(ctx).OAuthSessions().RevokeRequest(ctx, requestID, time.Now().UTC())
}
func (storage *Storage) CreatePKCERequestSession(ctx context.Context, signature string, request fosite.Requester) error {
	return storage.create(ctx, kindPKCE, signature, request, fosite.AuthorizeCode)
}
func (storage *Storage) GetPKCERequestSession(ctx context.Context, signature string, _ fosite.Session) (fosite.Requester, error) {
	return storage.get(ctx, kindPKCE, signature, nil)
}
func (storage *Storage) DeletePKCERequestSession(ctx context.Context, signature string) error {
	return storage.repositoriesFor(ctx).OAuthSessions().Delete(ctx, kindPKCE, signatureHash(signature))
}
func (storage *Storage) CreateOpenIDConnectSession(ctx context.Context, code string, request fosite.Requester) error {
	return storage.create(ctx, kindOIDC, code, request, fosite.AuthorizeCode)
}
func (storage *Storage) GetOpenIDConnectSession(ctx context.Context, code string, _ fosite.Requester) (fosite.Requester, error) {
	return storage.get(ctx, kindOIDC, code, nil)
}
func (storage *Storage) DeleteOpenIDConnectSession(ctx context.Context, code string) error {
	return storage.repositoriesFor(ctx).OAuthSessions().Delete(ctx, kindOIDC, signatureHash(code))
}
