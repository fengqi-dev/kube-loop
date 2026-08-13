package httpauth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	provider.state, provider.nonce = state, nonce
	provider.mu.Unlock()
	return "https://identity.example.test/authorize?state=" + url.QueryEscape(state) +
		"&code_challenge=" + url.QueryEscape(challenge), nil
}
func (provider *handlerOIDCProvider) Exchange(_ context.Context, code, verifier, nonce string) (authn.Identity, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if code == "" || verifier == "" || nonce != provider.nonce {
		return authn.Identity{}, login.ErrInvalidRequest
	}
	return authn.Identity{ProviderID: "corp", Issuer: "https://identity.example.test", Subject: "subject",
		DisplayName: "Ada", Email: "ada@example.test", Groups: []string{"developers"}}, nil
}

func TestOIDCAuthorizationCodePKCELifecycle(t *testing.T) {
	handler, provider := newHandlerTestServer(t)
	verifier := strings.Repeat("v", 43)
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	callbackURI := "http://127.0.0.1:49152/callback"
	state, nonce := strings.Repeat("s", 43), strings.Repeat("n", 43)
	authorizeQuery := url.Values{
		"response_type": {"code"}, "client_id": {login.DefaultDesktopClientID}, "redirect_uri": {callbackURI},
		"scope": {"openid profile email offline_access kubeloop.api"}, "state": {state}, "nonce": {nonce},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "provider": {"corp"},
	}
	authorize := perform(t, handler, http.MethodGet, "/oauth2/authorize?"+authorizeQuery.Encode(), nil)
	if authorize.Code != http.StatusFound || authorize.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("authorize status=%d body=%s", authorize.Code, authorize.Body.String())
	}
	provider.mu.Lock()
	upstreamState := provider.state
	provider.mu.Unlock()
	callback := perform(t, handler, http.MethodGet,
		"/oauth2/callback/corp?code=upstream-code&state="+url.QueryEscape(upstreamState), nil)
	if callback.Code != http.StatusSeeOther {
		t.Fatalf("callback status=%d body=%s", callback.Code, callback.Body.String())
	}
	redirect, err := url.Parse(callback.Header().Get("Location"))
	if err != nil || redirect.Query().Get("state") != state {
		t.Fatalf("callback redirect=%q error=%v", callback.Header().Get("Location"), err)
	}
	tokenForm := url.Values{"grant_type": {"authorization_code"}, "code": {redirect.Query().Get("code")},
		"code_verifier": {verifier}, "client_id": {login.DefaultDesktopClientID},
		"redirect_uri": {callbackURI}, "device_id": {"desktop-test"}}
	exchange := performForm(t, handler, "/oauth2/token", tokenForm)
	if exchange.Code != http.StatusOK {
		t.Fatalf("token status=%d body=%s", exchange.Code, exchange.Body.String())
	}
	var tokens tokenResponse
	if err := json.Unmarshal(exchange.Body.Bytes(), &tokens); err != nil || tokens.AccessToken == "" ||
		tokens.RefreshToken == "" || tokens.IDToken == "" || tokens.ExpiresIn <= 0 {
		t.Fatalf("tokens=%#v error=%v", tokens, err)
	}
	if replay := performForm(t, handler, "/oauth2/token", tokenForm); replay.Code != http.StatusBadRequest {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	userinfoRequest := httptest.NewRequest(http.MethodGet, "/oauth2/userinfo", nil)
	userinfoRequest.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	userinfo := httptest.NewRecorder()
	handler.ServeHTTP(userinfo, userinfoRequest)
	if userinfo.Code != http.StatusOK || !strings.Contains(userinfo.Body.String(), `"name":"Ada"`) {
		t.Fatalf("userinfo status=%d body=%s", userinfo.Code, userinfo.Body.String())
	}
	refresh := performForm(t, handler, "/oauth2/token", url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {tokens.RefreshToken}, "client_id": {login.DefaultDesktopClientID},
	})
	if refresh.Code != http.StatusOK {
		t.Fatalf("refresh status=%d body=%s", refresh.Code, refresh.Body.String())
	}
}

func TestOIDCDiscoveryJWKSAndRevocation(t *testing.T) {
	handler, _ := newHandlerTestServer(t)
	for _, path := range []string{openidConfigurationPath, oauthMetadataPath, "/oauth2/jwks"} {
		response := perform(t, handler, http.MethodGet, path, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	revoke := performForm(t, handler, "/oauth2/revoke", url.Values{"token": {"not-a-token"}})
	if revoke.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", revoke.Code, revoke.Body.String())
	}
	invalidRequest := httptest.NewRequest(http.MethodPost, "/oauth2/token", strings.NewReader(`{"grant_type":"refresh_token"}`))
	invalidRequest.Header.Set("Content-Type", "application/json")
	invalid := performRequest(handler, invalidRequest)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid content type status=%d", invalid.Code)
	}
}

func TestLocalAccountAuthorizationUsesSameOAuthCodeFlowAndMFA(t *testing.T) {
	handler, _ := newHandlerTestServer(t)
	verifier := strings.Repeat("v", 43)
	digest := sha256.Sum256([]byte(verifier))
	callbackURI := "http://127.0.0.1:49152/callback"
	query := url.Values{
		"response_type": {"code"}, "client_id": {login.DefaultDesktopClientID}, "redirect_uri": {callbackURI},
		"scope": {"openid kubeloop.api"}, "state": {strings.Repeat("s", 43)}, "nonce": {strings.Repeat("n", 43)},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "code_challenge_method": {"S256"},
		"provider": {"local"},
	}
	page := perform(t, handler, http.MethodGet, "/oauth2/authorize?"+query.Encode(), nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `action="/oauth2/login/local"`) {
		t.Fatalf("local authorize status=%d body=%s", page.Code, page.Body.String())
	}
	if !strings.Contains(page.Body.String(), `href="/oauth2/assets/login.css"`) ||
		!strings.Contains(page.Header().Get("Content-Security-Policy"), "style-src 'self'") {
		t.Fatalf("local login page does not load its same-origin shadcn styles")
	}
	styles := perform(t, handler, http.MethodGet, "/oauth2/assets/login.css", nil)
	if styles.Code != http.StatusOK || !strings.HasPrefix(styles.Header().Get("Content-Type"), "text/css") ||
		!strings.Contains(styles.Body.String(), "--primary:") {
		t.Fatalf("local login styles status=%d content-type=%q", styles.Code, styles.Header().Get("Content-Type"))
	}
	marker := `name="transaction" value="`
	start := strings.Index(page.Body.String(), marker)
	if start < 0 {
		t.Fatal("local transaction is missing")
	}
	transaction := page.Body.String()[start+len(marker):]
	transaction = transaction[:strings.IndexByte(transaction, '"')]
	loginResponse := performForm(t, handler, "/oauth2/login/local", url.Values{
		"transaction": {transaction}, "username": {"admin"}, "password": {"correct-password"},
		"second_factor": {"123456"},
	})
	if loginResponse.Code != http.StatusSeeOther {
		t.Fatalf("local login status=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}
	redirect, err := url.Parse(loginResponse.Header().Get("Location"))
	if err != nil || redirect.Query().Get("code") == "" {
		t.Fatalf("local redirect=%q error=%v", loginResponse.Header().Get("Location"), err)
	}
	tokens := performForm(t, handler, "/oauth2/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {redirect.Query().Get("code")},
		"code_verifier": {verifier}, "client_id": {login.DefaultDesktopClientID}, "redirect_uri": {callbackURI},
	})
	if tokens.Code != http.StatusOK || !strings.Contains(tokens.Body.String(), `"id_token"`) {
		t.Fatalf("local token status=%d body=%s", tokens.Code, tokens.Body.String())
	}
}

func newHandlerTestServer(t *testing.T) (*echo.Echo, *handlerOIDCProvider) {
	t.Helper()
	store, err := storage.Open(t.Context(), storage.Config{Backend: storage.BackendSQLite,
		SQLitePath: filepath.Join(t.TempDir(), "http-auth.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	provider := &handlerOIDCProvider{}
	anonymousProvider, err := development.NewAnonymous("guest", "Anonymous", development.IdentityConfig{Subject: "guest"})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := authn.NewRegistry(provider, anonymousProvider)
	if err != nil {
		t.Fatal(err)
	}
	loginService, err := login.New(registry, store, login.Config{Clients: []login.Client{{
		ID: login.DefaultDesktopClientID, AllowLoopback: true,
		Scopes: []string{"openid", "profile", "email", "offline_access", "kubeloop.api"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	_, signingKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tokenService, err := token.New(store, token.Config{Issuer: "https://gateway.example.test", KeyID: "test", SigningKey: signingKey})
	if err != nil {
		t.Fatal(err)
	}
	localPrincipal, err := store.Principals().Upsert(t.Context(), storage.Principal{
		ID: "b67fa049-a785-4d14-bcc7-e95289499591", Provider: "local", ExternalID: "admin",
		DisplayName: "Administrator", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(loginService, tokenService, WithLocalAuthenticator(func(
		_ context.Context, username string, password []byte, secondFactor, _ string,
	) (storage.Principal, error) {
		if username != "admin" || string(password) != "correct-password" || secondFactor != "123456" {
			return storage.Principal{}, errors.New("invalid credentials")
		}
		return localPrincipal, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	router := echo.New()
	NewRoutes(service).RegisterRoutes(router.Group(""))
	return router, provider
}

func performForm(t *testing.T, handler http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return performRequest(handler, request)
}

func perform(t *testing.T, handler http.Handler, method, path string, body *strings.Reader) *httptest.ResponseRecorder {
	t.Helper()
	var request *http.Request
	if body == nil {
		request = httptest.NewRequest(method, path, nil)
	} else {
		request = httptest.NewRequest(method, path, body)
	}
	return performRequest(handler, request)
}

func performRequest(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
