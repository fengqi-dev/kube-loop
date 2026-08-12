package oidc

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	coreoidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
	"golang.org/x/oauth2"
)

var pkceValuePattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

type discoveryMetadata struct {
	Issuer                           string   `json:"issuer"`
	ClaimsSupported                  []string `json:"claims_supported"`
	CodeChallengeMethodsSupported    []string `json:"code_challenge_methods_supported"`
	IDTokenSigningAlgValuesSupported []string `json:"id_token_signing_alg_values_supported"`
}

type Provider struct {
	descriptor authn.Descriptor
	issuer     string
	claims     ClaimMapping
	httpClient *http.Client
	oauth2     oauth2.Config
	verifier   *coreoidc.IDTokenVerifier
}

var _ authn.Provider = (*Provider)(nil)
var _ authn.AuthorizationCodeProvider = (*Provider)(nil)

func New(ctx context.Context, config Config) (*Provider, error) {
	normalized, err := config.normalized()
	if err != nil {
		return nil, err
	}
	discoveryContext := coreoidc.ClientContext(ctx, normalized.HTTPClient)
	upstream, err := coreoidc.NewProvider(discoveryContext, normalized.Issuer)
	if err != nil {
		return nil, errors.New("discover OIDC provider")
	}
	var metadata discoveryMetadata
	if err := upstream.Claims(&metadata); err != nil {
		return nil, errors.New("decode OIDC discovery metadata")
	}
	if metadata.Issuer != normalized.Issuer {
		return nil, errors.New("OIDC discovery issuer mismatch")
	}
	if !slices.Contains(metadata.CodeChallengeMethodsSupported, "S256") {
		return nil, errors.New("OIDC provider does not advertise PKCE S256 support")
	}
	if !hasIntersection(normalized.AllowedSigningAlgs, metadata.IDTokenSigningAlgValuesSupported) {
		return nil, errors.New("OIDC provider has no allowed ID token signing algorithm")
	}
	if len(metadata.ClaimsSupported) > 0 {
		for _, claim := range normalized.RequiredClaims {
			if !slices.Contains(metadata.ClaimsSupported, claim) {
				return nil, fmt.Errorf("OIDC provider does not advertise required claim %q", claim)
			}
		}
	}
	endpoint := upstream.Endpoint()
	if err := requireEndpointHTTPS("authorization", endpoint.AuthURL); err != nil {
		return nil, err
	}
	if err := requireEndpointHTTPS("token", endpoint.TokenURL); err != nil {
		return nil, err
	}
	return &Provider{
		descriptor: authn.Descriptor{
			ID: normalized.ID, Type: authn.ProviderOIDC,
			DisplayName: normalized.DisplayName, Interaction: authn.InteractionBrowser,
		},
		issuer:     normalized.Issuer,
		claims:     normalized.Claims,
		httpClient: normalized.HTTPClient,
		oauth2: oauth2.Config{
			ClientID: normalized.ClientID, ClientSecret: normalized.ClientSecret,
			RedirectURL: normalized.RedirectURL, Endpoint: endpoint, Scopes: normalized.Scopes,
		},
		verifier: upstream.Verifier(&coreoidc.Config{
			ClientID: normalized.ClientID, SupportedSigningAlgs: normalized.AllowedSigningAlgs,
		}),
	}, nil
}

func (provider *Provider) Descriptor() authn.Descriptor { return provider.descriptor }

// Check confirms that construction completed. Discovery is validated during
// New; JWKS refreshes are handled by the long-lived verifier on key rotation.
func (provider *Provider) Check(context.Context) error {
	if provider == nil || provider.verifier == nil {
		return errors.New("OIDC provider is not initialized")
	}
	return nil
}

func (provider *Provider) AuthorizationURL(state, nonce, pkceChallenge string) (string, error) {
	if len(state) < 32 || len(nonce) < 32 {
		return "", errors.New("OIDC state and nonce must contain at least 32 characters")
	}
	if !pkceValuePattern.MatchString(pkceChallenge) {
		return "", errors.New("OIDC PKCE S256 challenge must be 43-128 URL-safe characters")
	}
	return provider.oauth2.AuthCodeURL(state,
		coreoidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", pkceChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	), nil
}

func (provider *Provider) Exchange(ctx context.Context, code, pkceVerifier, expectedNonce string) (authn.Identity, error) {
	code = strings.TrimSpace(code)
	if code == "" || !pkceValuePattern.MatchString(pkceVerifier) || len(expectedNonce) < 32 {
		return authn.Identity{}, errors.New("invalid OIDC code exchange parameters")
	}
	requestContext := coreoidc.ClientContext(ctx, provider.httpClient)
	token, err := provider.oauth2.Exchange(requestContext, code, oauth2.VerifierOption(pkceVerifier))
	if err != nil {
		return authn.Identity{}, errors.New("exchange OIDC authorization code")
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return authn.Identity{}, errors.New("OIDC token response has no ID token")
	}
	idToken, err := provider.verifier.Verify(requestContext, rawIDToken)
	if err != nil {
		return authn.Identity{}, errors.New("verify OIDC ID token")
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(expectedNonce)) != 1 {
		return authn.Identity{}, errors.New("OIDC nonce mismatch")
	}
	var rawClaims map[string]any
	if err := idToken.Claims(&rawClaims); err != nil {
		return authn.Identity{}, errors.New("decode OIDC claims")
	}
	return authn.Identity{
		ProviderID:  provider.descriptor.ID,
		Issuer:      idToken.Issuer,
		Subject:     idToken.Subject,
		DisplayName: stringClaim(rawClaims, provider.claims.DisplayName),
		Email:       stringClaim(rawClaims, provider.claims.Email),
		Groups:      stringListClaim(rawClaims, provider.claims.Groups),
	}, nil
}

func requireEndpointHTTPS(name, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("OIDC %s endpoint must be an absolute HTTPS URL", name)
	}
	return nil
}

func hasIntersection(left, right []string) bool {
	for _, value := range left {
		if slices.Contains(right, value) {
			return true
		}
	}
	return false
}

func stringClaim(claims map[string]any, name string) string {
	value, _ := claimValue(claims, name).(string)
	return strings.TrimSpace(value)
}

func stringListClaim(claims map[string]any, name string) []string {
	switch value := claimValue(claims, name).(type) {
	case []any:
		groups := make([]string, 0, len(value))
		for _, item := range value {
			if group, ok := item.(string); ok && strings.TrimSpace(group) != "" {
				groups = append(groups, strings.TrimSpace(group))
			}
		}
		return groups
	case []string:
		return slices.Clone(value)
	case string:
		if strings.TrimSpace(value) != "" {
			return []string{strings.TrimSpace(value)}
		}
	}
	return nil
}

// claimValue first resolves the configured name literally so URI-style Auth0
// custom claims and names containing dots remain valid. If no literal claim
// exists, dot notation traverses JSON objects such as Keycloak's
// realm_access.roles.
func claimValue(claims map[string]any, name string) any {
	if value, exists := claims[name]; exists {
		return value
	}
	parts := strings.Split(name, ".")
	if len(parts) < 2 {
		return nil
	}
	var current any = claims
	for _, part := range parts {
		object, ok := current.(map[string]any)
		if !ok || part == "" {
			return nil
		}
		current, ok = object[part]
		if !ok {
			return nil
		}
	}
	return current
}
