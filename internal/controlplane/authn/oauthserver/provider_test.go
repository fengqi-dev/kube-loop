package oauthserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	controlstorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func TestClientCredentialsIssuesOpaqueMachineToken(t *testing.T) {
	endpoints, store, identity := newTestEndpoints(t)
	createTestClient(t, store, controlstorage.OAuthClient{ID: "machine", Name: "Machine", Public: false,
		GrantTypes: []string{"client_credentials"}, Scopes: []string{"kubeloop.api"}, Enabled: true,
		MachineIdentityID: identity.ID}, "correct-secret")
	response := requestToken(endpoints, url.Values{"grant_type": {"client_credentials"}, "scope": {"kubeloop.api"},
		"client_id": {"machine"}, "client_secret": {"correct-secret"}})
	if response.Code != http.StatusOK {
		t.Fatalf("token status = %d body=%s", response.Code, response.Body.String())
	}
	var document struct {
		AccessToken  string `json:"access_token"`
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.AccessToken == "" || document.IDToken != "" || document.RefreshToken != "" {
		t.Fatalf("unexpected token response %#v", document)
	}
	session, _, err := endpoints.IntrospectAccessToken(context.Background(), document.AccessToken)
	if err != nil || !session.Machine || session.IdentityID != identity.ID {
		t.Fatalf("introspection = %#v, %v", session, err)
	}
}

func TestDesktopAuthorizationCodeRequiresPKCES256RotatesRefreshAndRejectsReplay(t *testing.T) {
	endpoints, store, identity := newTestEndpoints(t)
	createTestClient(t, store, controlstorage.OAuthClient{ID: controlstorage.DesktopOAuthClientID, Name: "Desktop", Public: true,
		RedirectURIs: []string{controlstorage.DesktopOAuthRedirectURI}, GrantTypes: []string{"authorization_code", "refresh_token"},
		Scopes: []string{"openid", "offline_access", "kubeloop.api"}, Trusted: true, Enabled: true}, "")
	verifier := strings.Repeat("v", 64)
	challengeSum := sha256.Sum256([]byte(verifier))
	query := url.Values{"response_type": {"code"}, "client_id": {controlstorage.DesktopOAuthClientID}, "redirect_uri": {controlstorage.DesktopOAuthRedirectURI},
		"scope": {"openid offline_access kubeloop.api"}, "state": {strings.Repeat("s", 32)}, "nonce": {strings.Repeat("n", 32)},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(challengeSum[:])}, "code_challenge_method": {"S256"}}
	authorize := httptest.NewRequest(http.MethodGet, "https://issuer.example/oauth2/authorize?"+query.Encode(), nil)
	challenge, err := endpoints.BeginAuthorization(authorize.Context(), authorize)
	if err != nil {
		t.Fatal(err)
	}
	browserIdentity := BrowserIdentity{Identity: identity, ProviderID: "local", AuthTime: time.Now().UTC()}
	recorder := httptest.NewRecorder()
	if err := endpoints.CompleteAuthorization(recorder, authorize, challenge.Transaction, challenge.CSRF, browserIdentity, true); err != nil {
		t.Fatal(err)
	}
	body := recorder.Body.String()
	policy := recorder.Header().Get("Content-Security-Policy")
	if recorder.Code != http.StatusOK || !strings.Contains(body, ">Login complete</h1>") ||
		strings.Contains(body, "登录完成") ||
		!strings.Contains(body, `method="post" action="/oauth2/logout"`) ||
		!strings.Contains(body, ">Log out</span>") || strings.Contains(body, "退出登录") ||
		!strings.Contains(policy, "form-action 'self'") || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("authorize response status=%d headers=%v body=%q", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	match := regexp.MustCompile(`href="(kubeloop:[^"]+)"`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("desktop callback link is missing: %q", recorder.Body.String())
	}
	redirect, err := url.Parse(html.UnescapeString(match[1]))
	if err != nil || redirect.Scheme != "kubeloop" || redirect.Host != "auth" || redirect.Path != "/callback" ||
		redirect.Query().Get("code") == "" || redirect.Query().Get("state") != strings.Repeat("s", 32) {
		t.Fatalf("authorize callback = %q: %v", html.UnescapeString(match[1]), err)
	}
	code := redirect.Query().Get("code")
	first := requestToken(endpoints, url.Values{"grant_type": {"authorization_code"}, "client_id": {controlstorage.DesktopOAuthClientID},
		"redirect_uri": {controlstorage.DesktopOAuthRedirectURI}, "code": {code}, "code_verifier": {verifier}})
	if first.Code != http.StatusOK {
		t.Fatalf("exchange status=%d body=%s", first.Code, first.Body.String())
	}
	var issued map[string]any
	_ = json.Unmarshal(first.Body.Bytes(), &issued)
	oldRefresh, _ := issued["refresh_token"].(string)
	if oldRefresh == "" || issued["access_token"] == nil || issued["id_token"] == nil {
		t.Fatalf("tokens=%#v", issued)
	}
	rotated := requestToken(endpoints, url.Values{"grant_type": {"refresh_token"}, "client_id": {controlstorage.DesktopOAuthClientID}, "refresh_token": {oldRefresh}})
	if rotated.Code != http.StatusOK {
		t.Fatalf("refresh status=%d body=%s", rotated.Code, rotated.Body.String())
	}
	var refreshed map[string]any
	_ = json.Unmarshal(rotated.Body.Bytes(), &refreshed)
	if refreshed["refresh_token"] == nil {
		t.Fatalf("rotated token=%#v", refreshed)
	}
	if replay := requestToken(endpoints, url.Values{"grant_type": {"refresh_token"}, "client_id": {controlstorage.DesktopOAuthClientID}, "refresh_token": {oldRefresh}}); replay.Code == http.StatusOK {
		t.Fatal("replayed refresh token was accepted")
	}
	if replay := requestToken(endpoints, url.Values{"grant_type": {"refresh_token"}, "client_id": {controlstorage.DesktopOAuthClientID}, "refresh_token": {refreshed["refresh_token"].(string)}}); replay.Code == http.StatusOK {
		t.Fatal("rotated grant remained active after refresh token replay")
	}
	if replay := requestToken(endpoints, url.Values{"grant_type": {"authorization_code"}, "client_id": {controlstorage.DesktopOAuthClientID},
		"redirect_uri": {controlstorage.DesktopOAuthRedirectURI}, "code": {code}, "code_verifier": {verifier}}); replay.Code == http.StatusOK {
		t.Fatal("replayed authorization code was accepted")
	}
	missing := url.Values{}
	for key, values := range query {
		missing[key] = append([]string(nil), values...)
	}
	missing.Del("code_challenge_method")
	request := httptest.NewRequest(http.MethodGet, "https://issuer.example/oauth2/authorize?"+missing.Encode(), nil)
	if _, err := endpoints.BeginAuthorization(request.Context(), request); err == nil {
		t.Fatal("authorization without PKCE S256 was accepted")
	}
}

func TestRemovedGrantTypesAreRejected(t *testing.T) {
	endpoints, _, _ := newTestEndpoints(t)
	for _, grant := range []string{"password", "implicit"} {
		t.Run(grant, func(t *testing.T) {
			response := requestToken(endpoints, url.Values{"grant_type": {grant}, "client_id": {controlstorage.DesktopOAuthClientID}})
			if response.Code == http.StatusOK {
				t.Fatalf("removed grant %q was accepted", grant)
			}
		})
	}
}

func TestBrowserSessionRevocationInvalidatesIdentity(t *testing.T) {
	endpoints, _, identity := newTestEndpoints(t)
	browserIdentity := BrowserIdentity{Identity: identity, ProviderID: "local", AuthTime: time.Now().UTC()}
	token, err := endpoints.CreateBrowserSession(t.Context(), browserIdentity, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := endpoints.BrowserIdentity(t.Context(), token); err != nil {
		t.Fatalf("browser identity before revocation: %v", err)
	}
	if err := endpoints.RevokeBrowserSession(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	if _, err := endpoints.BrowserIdentity(t.Context(), token); err == nil {
		t.Fatal("revoked browser session remained valid")
	}
	if err := endpoints.RevokeBrowserSession(t.Context(), token); err != nil {
		t.Fatalf("repeated browser logout must be idempotent: %v", err)
	}
}

func requestToken(endpoints *Endpoints, form url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "https://issuer.example/oauth2/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	endpoints.Token(recorder, request)
	return recorder
}

func newTestEndpoints(t *testing.T) (*Endpoints, *controlstorage.Store, controlstorage.Identity) {
	t.Helper()
	store, err := controlstorage.Open(t.Context(), controlstorage.Config{Backend: controlstorage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "oauth.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	identity, err := store.Identities().Create(t.Context(), controlstorage.Identity{ID: uuid.NewString(), Type: "human", DisplayName: "Admin", PrimaryEmail: "admin@example.test", Status: "active", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fositeStorage, err := NewStorage(store)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewProvider(fositeStorage, Config{Issuer: "https://issuer.example", HMACSecret: []byte("01234567890123456789012345678901"), SigningKey: key, AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	endpoints, err := NewEndpoints(provider, store, "test-key", key)
	if err != nil {
		t.Fatal(err)
	}
	return endpoints, store, identity
}

func createTestClient(t *testing.T, store *controlstorage.Store, client controlstorage.OAuthClient, secret string) {
	t.Helper()
	now := time.Now().UTC()
	client.CreatedAt, client.UpdatedAt = now, now
	if err := store.OAuthClients().Create(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	if secret == "" {
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.OAuthClients().SetSecret(t.Context(), controlstorage.OAuthClientSecret{ClientID: client.ID, SecretHash: hash, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
}
