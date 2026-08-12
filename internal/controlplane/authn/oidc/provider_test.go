package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

type testProviderMetadata struct {
	PKCEMethods  []string
	Algorithms   []string
	Claims       []string
	IssuerSuffix string
}

func TestProviderDiscoveryAuthorizationAndExchange(t *testing.T) {
	nonce := strings.Repeat("n", 43)
	verifier := strings.Repeat("v", 43)
	challenge := strings.Repeat("c", 43)
	provider, tokenRequests := newTestProvider(t, testProviderMetadata{
		PKCEMethods: []string{"S256"}, Algorithms: []string{"RS256"},
		Claims: []string{"sub", "name", "email", "groups"},
	}, nonce)

	authorizationURL, err := provider.AuthorizationURL(strings.Repeat("s", 43), nonce, challenge)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("response_type") != "code" || query.Get("code_challenge_method") != "S256" ||
		query.Get("code_challenge") != challenge || query.Get("nonce") != nonce {
		t.Fatalf("authorization query = %v", query)
	}

	identity, err := provider.Exchange(context.Background(), "authorization-code", verifier, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if identity.ProviderID != "corporate" || identity.Subject != "immutable-subject" ||
		identity.DisplayName != "Ada Lovelace" || identity.Email != "ada@example.test" {
		t.Fatalf("identity = %#v", identity)
	}
	if len(identity.Groups) != 2 || identity.Groups[0] != "developers" || identity.Groups[1] != "platform" {
		t.Fatalf("groups = %#v", identity.Groups)
	}
	select {
	case request := <-tokenRequests:
		if request.Get("code_verifier") != verifier || request.Get("code") != "authorization-code" {
			t.Fatalf("token request = %v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("token endpoint was not called")
	}
}

func TestProviderRejectsUnsafeOrIncompleteDiscovery(t *testing.T) {
	tests := []struct {
		name     string
		metadata testProviderMetadata
	}{
		{name: "missing S256", metadata: testProviderMetadata{Algorithms: []string{"RS256"}, Claims: []string{"sub"}}},
		{name: "algorithm mismatch", metadata: testProviderMetadata{PKCEMethods: []string{"S256"}, Algorithms: []string{"ES256"}, Claims: []string{"sub"}}},
		{name: "missing required claim", metadata: testProviderMetadata{PKCEMethods: []string{"S256"}, Algorithms: []string{"RS256"}, Claims: []string{"email"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newDiscoveryServer(t, test.metadata, nil, nil)
			defer server.Close()
			_, err := New(context.Background(), Config{
				ID: "corporate", Issuer: server.URL, ClientID: "client",
				RedirectURL: "https://gateway.example.test/auth/callback/corporate",
				HTTPClient:  server.Client(),
			})
			if err == nil {
				t.Fatal("expected discovery validation failure")
			}
		})
	}
}

func TestProviderAcceptsExactIssuerWithTrailingSlash(t *testing.T) {
	nonce := strings.Repeat("n", 43)
	provider, _ := newTestProvider(t, testProviderMetadata{
		PKCEMethods: []string{"S256"}, Algorithms: []string{"RS256"},
		Claims: []string{"sub"}, IssuerSuffix: "/",
	}, nonce)
	identity, err := provider.Exchange(context.Background(), "authorization-code", strings.Repeat("v", 43), nonce)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(identity.Issuer, "/") {
		t.Fatalf("issuer = %q", identity.Issuer)
	}
}

func TestConfigRejectsUnsafeValues(t *testing.T) {
	tests := []Config{
		{ID: "corp", Issuer: "http://login.example.test", ClientID: "client", RedirectURL: "https://gateway.example.test/callback"},
		{ID: "corp", Issuer: "https://login.example.test", ClientID: "client", RedirectURL: "http://gateway.example.test/callback"},
		{ID: "corp", Issuer: "https://login.example.test", ClientID: "client", RedirectURL: "https://gateway.example.test/callback", AllowedSigningAlgs: []string{"HS256"}},
		{ID: "corp", Issuer: "https://login.example.test", ClientID: "client", ClientSecret: "one", ClientSecretFile: "two", RedirectURL: "https://gateway.example.test/callback"},
	}
	for _, config := range tests {
		if _, err := config.normalized(); err == nil {
			t.Fatalf("config succeeded: %#v", config)
		}
	}
}

func TestConfigPreservesIssuerTrailingSlash(t *testing.T) {
	config, err := (Config{
		ID: "auth0", Issuer: "https://tenant.auth0.com/", ClientID: "client",
		RedirectURL: "https://gateway.example.test/oauth2/callback/auth0",
	}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	if config.Issuer != "https://tenant.auth0.com/" {
		t.Fatalf("issuer = %q", config.Issuer)
	}
}

func TestClaimMappingSupportsExactAndNestedClaims(t *testing.T) {
	claims := map[string]any{
		"profile.name":                        "literal name",
		"profile":                             map[string]any{"name": "nested name"},
		"realm_access":                        map[string]any{"roles": []any{"developer", "operator"}},
		"https://kubeloop.example.com/groups": []any{"platform", "security"},
	}
	if actual := stringClaim(claims, "profile.name"); actual != "literal name" {
		t.Fatalf("exact claim = %q", actual)
	}
	if actual := stringListClaim(claims, "realm_access.roles"); !slices.Equal(actual, []string{"developer", "operator"}) {
		t.Fatalf("nested groups = %#v", actual)
	}
	if actual := stringListClaim(claims, "https://kubeloop.example.com/groups"); !slices.Equal(actual, []string{"platform", "security"}) {
		t.Fatalf("namespaced groups = %#v", actual)
	}
}

func TestAuthorizationAndExchangeRejectWeakBindingValues(t *testing.T) {
	provider, _ := newTestProvider(t, testProviderMetadata{
		PKCEMethods: []string{"S256"}, Algorithms: []string{"RS256"}, Claims: []string{"sub"},
	}, strings.Repeat("n", 43))
	if _, err := provider.AuthorizationURL("short", strings.Repeat("n", 43), strings.Repeat("c", 43)); err == nil {
		t.Fatal("expected weak state rejection")
	}
	if _, err := provider.Exchange(context.Background(), "code", "short", strings.Repeat("n", 43)); err == nil {
		t.Fatal("expected weak verifier rejection")
	}
}

func newTestProvider(t *testing.T, metadata testProviderMetadata, nonce string) (*Provider, <-chan url.Values) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: privateKey}, &jose.SignerOptions{
		ExtraHeaders: map[jose.HeaderKey]any{jose.HeaderKey("kid"): "test-key"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tokenRequests := make(chan url.Values, 2)
	server := newDiscoveryServer(t, metadata, &privateKey.PublicKey, func(issuer string, request *http.Request) string {
		if err := request.ParseForm(); err != nil {
			return ""
		}
		tokenRequests <- request.Form
		now := time.Now()
		raw, err := jwt.Signed(signer).Claims(jwt.Claims{
			Issuer: issuer, Subject: "immutable-subject", Audience: jwt.Audience{"client"},
			Expiry: jwt.NewNumericDate(now.Add(time.Minute)), IssuedAt: jwt.NewNumericDate(now),
		}).Claims(map[string]any{
			"nonce": nonce, "name": "Ada Lovelace", "email": "ada@example.test",
			"groups": []string{"developers", "platform"},
		}).Serialize()
		if err != nil {
			t.Errorf("sign ID token: %v", err)
			return ""
		}
		return raw
	})
	t.Cleanup(server.Close)
	provider, err := New(context.Background(), Config{
		ID: "corporate", DisplayName: "Corporate SSO", Issuer: server.URL + metadata.IssuerSuffix,
		ClientID: "client", ClientSecret: "secret",
		RedirectURL: "https://gateway.example.test/auth/callback/corporate",
		HTTPClient:  server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider, tokenRequests
}

func newDiscoveryServer(
	t *testing.T,
	metadata testProviderMetadata,
	publicKey *rsa.PublicKey,
	token func(string, *http.Request) string,
) *httptest.Server {
	t.Helper()
	if publicKey == nil {
		privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		publicKey = &privateKey.PublicKey
	}
	publicJWK := jose.JSONWebKey{Key: publicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}
	var server *httptest.Server
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"issuer": server.URL + metadata.IssuerSuffix, "authorization_endpoint": server.URL + "/authorize",
				"token_endpoint": server.URL + "/token", "jwks_uri": server.URL + "/jwks",
				"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
				"id_token_signing_alg_values_supported": metadata.Algorithms,
				"code_challenge_methods_supported":      metadata.PKCEMethods, "claims_supported": metadata.Claims,
			})
		case "/jwks":
			_ = json.NewEncoder(writer).Encode(map[string]any{"keys": []jose.JSONWebKey{publicJWK}})
		case "/token":
			if token == nil {
				http.Error(writer, "not configured", http.StatusBadRequest)
				return
			}
			raw := token(server.URL+metadata.IssuerSuffix, request)
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"access_token": "access-token", "token_type": "Bearer", "expires_in": 60, "id_token": raw,
			})
		default:
			http.NotFound(writer, request)
		}
	})
	server = httptest.NewTLSServer(handler)
	return server
}
