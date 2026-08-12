package httpauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/development"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/login"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/token"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/labstack/echo/v5"
)

type handlerOIDCProvider struct {
	mu    sync.Mutex
	state string
	nonce string
}

func (provider *handlerOIDCProvider) Descriptor() authn.Descriptor {
	return authn.Descriptor{ID: "corp", Type: authn.ProviderOIDC, Interaction: authn.InteractionBrowser}
}
func (provider *handlerOIDCProvider) Check(context.Context) error { return nil }
func (provider *handlerOIDCProvider) AuthorizationURL(state, nonce, challenge string) (string, error) {
	provider.mu.Lock()
	provider.state = state
	provider.nonce = nonce
	provider.mu.Unlock()
	return "https://identity.example.test/authorize?state=" + url.QueryEscape(state) + "&code_challenge=" + url.QueryEscape(challenge), nil
}
func (provider *handlerOIDCProvider) Exchange(_ context.Context, code, verifier, nonce string) (authn.Identity, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if code == "" || verifier == "" || nonce != provider.nonce {
		return authn.Identity{}, login.ErrInvalidRequest
	}
	return authn.Identity{
		ProviderID: "corp", Issuer: "https://identity.example.test", Subject: "subject",
		DisplayName: "Ada", Groups: []string{"developers"},
	}, nil
}

func TestHTTPAuthenticationVerticalSlice(t *testing.T) {
	handler, provider := newHandlerTestServer(t)
	verifier := strings.Repeat("v", 43)
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	startBody := map[string]string{
		"clientCallback": "http://127.0.0.1:49152/callback",
		"state":          strings.Repeat("s", 43), "nonce": strings.Repeat("n", 43),
		"pkceChallenge": challenge,
	}
	start := performJSON(t, handler, http.MethodPost, "/auth/oidc/corp/start", startBody)
	if start.Code != http.StatusOK || start.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("start status=%d body=%s", start.Code, start.Body.String())
	}
	var started startResponse
	if err := json.Unmarshal(start.Body.Bytes(), &started); err != nil || started.AuthorizationURL == "" {
		t.Fatalf("start response=%#v error=%v", started, err)
	}
	provider.mu.Lock()
	upstreamState := provider.state
	provider.mu.Unlock()

	callbackRequest := httptest.NewRequest(http.MethodGet,
		"/auth/callback/corp?code=upstream-code&state="+url.QueryEscape(upstreamState), nil)
	callback := httptest.NewRecorder()
	serveHTTP(handler, callback, callbackRequest)
	if callback.Code != http.StatusSeeOther {
		t.Fatalf("callback status=%d body=%s", callback.Code, callback.Body.String())
	}
	redirect, err := url.Parse(callback.Header().Get("Location"))
	if err != nil || redirect.Host != "127.0.0.1:49152" || redirect.Query().Get("state") != startBody["state"] {
		t.Fatalf("callback redirect=%q error=%v", callback.Header().Get("Location"), err)
	}

	exchange := performJSON(t, handler, http.MethodPost, "/auth/token/exchange", exchangeRequest{
		Code: redirect.Query().Get("code"), PKCEVerifier: verifier, DeviceID: "desktop-test",
	})
	if exchange.Code != http.StatusOK {
		t.Fatalf("exchange status=%d body=%s", exchange.Code, exchange.Body.String())
	}
	var tokens tokenResponse
	if err := json.Unmarshal(exchange.Body.Bytes(), &tokens); err != nil {
		t.Fatal(err)
	}
	if tokens.TokenType != "Bearer" || tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatalf("token response=%#v", tokens)
	}
	replay := performJSON(t, handler, http.MethodPost, "/auth/token/exchange", exchangeRequest{
		Code: redirect.Query().Get("code"), PKCEVerifier: verifier, DeviceID: "desktop-test",
	})
	if replay.Code != http.StatusBadRequest || strings.Contains(replay.Body.String(), redirect.Query().Get("code")) {
		t.Fatalf("exchange replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	refresh := performJSON(t, handler, http.MethodPost, "/auth/token/refresh", refreshRequest{RefreshToken: tokens.RefreshToken})
	if refresh.Code != http.StatusOK {
		t.Fatalf("refresh status=%d body=%s", refresh.Code, refresh.Body.String())
	}
}

func TestHTTPAuthenticationRejectsUnknownJSONAndHidesRevocationExistence(t *testing.T) {
	handler, _ := newHandlerTestServer(t)
	request := httptest.NewRequest(http.MethodPost, "/auth/token/refresh", bytes.NewBufferString(`{"refreshToken":"secret","unknown":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	serveHTTP(handler, response, request)
	if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("strict JSON status=%d body=%s", response.Code, response.Body.String())
	}
	oversizedSecret := strings.Repeat("oversized-auth-secret", maxAuthBodyBytes)
	oversizedRequest := httptest.NewRequest(http.MethodPost, "/auth/token/refresh", strings.NewReader(oversizedSecret))
	oversizedRequest.Header.Set("Content-Type", "application/json")
	oversizedResponse := httptest.NewRecorder()
	serveHTTP(handler, oversizedResponse, oversizedRequest)
	if oversizedResponse.Code != http.StatusRequestEntityTooLarge || strings.Contains(oversizedResponse.Body.String(), "oversized-auth-secret") {
		t.Fatalf("oversized auth status=%d body=%s", oversizedResponse.Code, oversizedResponse.Body.String())
	}
	revoke := performJSON(t, handler, http.MethodPost, "/auth/token/revoke", refreshRequest{RefreshToken: "not-a-token"})
	if revoke.Code != http.StatusNoContent {
		t.Fatalf("idempotent revoke status=%d body=%s", revoke.Code, revoke.Body.String())
	}
}

func TestHTTPAnonymousLoginIssuesStandardTokenLifecycle(t *testing.T) {
	handler, _ := newHandlerTestServer(t)
	anonymous := performJSON(t, handler, http.MethodPost, "/auth/anonymous/guest/login", anonymousRequest{
		DeviceID: "desktop-anonymous",
	})
	if anonymous.Code != http.StatusOK {
		t.Fatalf("anonymous login status=%d body=%s", anonymous.Code, anonymous.Body.String())
	}
	var anonymousTokens tokenResponse
	if err := json.Unmarshal(anonymous.Body.Bytes(), &anonymousTokens); err != nil ||
		anonymousTokens.AccessToken == "" || anonymousTokens.RefreshToken == "" {
		t.Fatalf("anonymous tokens=%#v error=%v", anonymousTokens, err)
	}
	refresh := performJSON(t, handler, http.MethodPost, "/auth/token/refresh", refreshRequest{
		RefreshToken: anonymousTokens.RefreshToken,
	})
	if refresh.Code != http.StatusOK {
		t.Fatalf("anonymous refresh status=%d body=%s", refresh.Code, refresh.Body.String())
	}
}

func newHandlerTestServer(t *testing.T) (*Service, *handlerOIDCProvider) {
	t.Helper()
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "http-auth.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	provider := &handlerOIDCProvider{}
	anonymousProvider, err := development.NewAnonymous(
		"guest", "Anonymous (unsafe)", development.IdentityConfig{Subject: "guest", Groups: []string{"developers"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := authn.NewRegistry(provider, anonymousProvider)
	if err != nil {
		t.Fatal(err)
	}
	loginService, err := login.New(registry, store, login.Config{})
	if err != nil {
		t.Fatal(err)
	}
	_, signingKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tokenService, err := token.New(store, token.Config{
		Issuer: "https://gateway.example.test", KeyID: "test", SigningKey: signingKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(loginService, tokenService)
	if err != nil {
		t.Fatal(err)
	}
	return handler, provider
}

func performJSON(t *testing.T, handler *Service, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	serveHTTP(handler, response, request)
	return response
}

func serveHTTP(service *Service, writer http.ResponseWriter, request *http.Request) {
	router := echo.New()
	NewRoutes(service).RegisterRoutes(router.Group("/auth"))
	router.ServeHTTP(writer, request)
}
