package oauthserver

import (
	"context"
	"errors"
	"time"

	"github.com/ory/fosite"
	"github.com/ory/fosite/handler/oauth2"
	"github.com/ory/fosite/handler/openid"
	"github.com/ory/fosite/handler/pkce"

	controlstorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
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
		return nil, errors.New("oauth repositories are required")
	}
	return &Storage{repositories: repositories}, nil
}

func (storage *Storage) repositoriesFor(
	ctx context.Context,
) controlstorage.Repositories {
	if transaction, ok := ctx.Value(transactionContextKey{}).(*transactionContext); ok &&
		transaction.transaction != nil {
		return transaction.transaction.Repositories()
	}
	return storage.repositories
}

func (storage *Storage) BeginTX(ctx context.Context) (context.Context, error) {
	manager, ok := storage.repositories.(controlstorage.ExplicitTransactionManager)
	if !ok {
		return context.WithValue(
			ctx,
			transactionContextKey{},
			&transactionContext{},
		), nil
	}
	transaction, err := manager.BeginTransaction(ctx)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(
		ctx,
		transactionContextKey{},
		&transactionContext{transaction: transaction},
	), nil
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

func (storage *Storage) GetClient(
	ctx context.Context,
	id string,
) (fosite.Client, error) {
	client, err := storage.repositoriesFor(ctx).OAuthClients().Get(ctx, id)
	if errors.Is(err, controlstorage.ErrNotFound) ||
		(err == nil && !client.Enabled) {
		return nil, fosite.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var secret []byte
	if !client.Public {
		stored, secretErr := storage.repositoriesFor(ctx).
			OAuthClients().
			GetSecret(ctx, id)
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
	return &fosite.DefaultOpenIDConnectClient{
		DefaultClient: &fosite.DefaultClient{
			ID: client.ID, Secret: secret, RedirectURIs: client.RedirectURIs, GrantTypes: client.GrantTypes,
			ResponseTypes: []string{
				responseTypeCode,
			}, Scopes: client.Scopes, Audience: []string{scopeKubeLoopAPI}, Public: client.Public,
		},
		TokenEndpointAuthMethod: authMethod,
	}, nil
}

func (*Storage) ClientAssertionJWTValid(
	context.Context,
	string,
) error {
	return fosite.ErrNotFound
}

func (*Storage) SetClientAssertionJWT(
	context.Context,
	string,
	time.Time,
) error {
	return fosite.ErrNotFound
}
