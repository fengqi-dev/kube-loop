package oauthserver

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"time"

	"github.com/ory/fosite"
	"github.com/ory/fosite/compose"
	"github.com/ory/fosite/token/jwt"
)

type Config struct {
	Issuer               string
	HMACSecret           []byte
	SigningKey           *ecdsa.PrivateKey
	AccessTokenTTL       time.Duration
	RefreshTokenTTL      time.Duration
	AuthorizationCodeTTL time.Duration
	IDTokenTTL           time.Duration
}

func NewProvider(storage *Storage, raw Config) (fosite.OAuth2Provider, error) {
	if storage == nil {
		return nil, errors.New("Fosite storage is required")
	}
	if len(raw.HMACSecret) != 32 {
		return nil, errors.New("Fosite HMAC secret must contain exactly 32 bytes")
	}
	if raw.SigningKey == nil || raw.SigningKey.Curve == nil || raw.SigningKey.Curve.Params().Name != "P-256" {
		return nil, errors.New("OIDC signing key must use ECDSA P-256")
	}
	if raw.AccessTokenTTL <= 0 || raw.RefreshTokenTTL <= 0 {
		return nil, errors.New("OAuth token lifetimes must be positive")
	}
	if raw.AuthorizationCodeTTL <= 0 {
		raw.AuthorizationCodeTTL = 10 * time.Minute
	}
	if raw.IDTokenTTL <= 0 {
		raw.IDTokenTTL = raw.AccessTokenTTL
	}
	config := &fosite.Config{
		AccessTokenLifespan: raw.AccessTokenTTL, RefreshTokenLifespan: raw.RefreshTokenTTL,
		AuthorizeCodeLifespan: raw.AuthorizationCodeTTL, IDTokenLifespan: raw.IDTokenTTL,
		IDTokenIssuer: raw.Issuer, AccessTokenIssuer: raw.Issuer, GlobalSecret: append([]byte(nil), raw.HMACSecret...),
		EnforcePKCE: true, EnforcePKCEForPublicClients: true, EnablePKCEPlainChallengeMethod: false,
		RefreshTokenScopes: []string{"offline_access"}, AllowedPromptValues: []string{"login", "none", "consent", "select_account"},
		SendDebugMessagesToClients: false, MinParameterEntropy: 32, TokenEntropy: 32,
		SanitationWhiteList: []string{"device_id"},
	}
	keyGetter := func(context.Context) (interface{}, error) { return raw.SigningKey, nil }
	strategy := &compose.CommonStrategy{
		CoreStrategy:               compose.NewOAuth2HMACStrategy(config),
		OpenIDConnectTokenStrategy: compose.NewOpenIDConnectStrategy(keyGetter, config),
		Signer:                     &jwt.DefaultSigner{GetPrivateKey: keyGetter},
	}
	return compose.Compose(config, storage, strategy,
		compose.OAuth2AuthorizeExplicitFactory,
		compose.OAuth2RefreshTokenGrantFactory,
		compose.OAuth2ClientCredentialsGrantFactory,
		compose.OpenIDConnectExplicitFactory,
		compose.OpenIDConnectRefreshFactory,
		compose.OAuth2TokenIntrospectionFactory,
		compose.OAuth2TokenRevocationFactory,
		compose.OAuth2PKCEFactory,
	), nil
}
