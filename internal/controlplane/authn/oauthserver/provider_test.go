package oauthserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	controlstorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
	"github.com/ory/fosite"
	"golang.org/x/crypto/bcrypt"
)

func TestClientCredentialsIssuesOpaqueMachineToken(t *testing.T) {
	endpoints, store, principal := newTestEndpoints(t, func(context.Context, string, []byte, string, string, string, string) (controlstorage.Principal, error) {
		return controlstorage.Principal{}, fosite.ErrNotFound
	})
	createTestClient(t, store, controlstorage.OAuthClient{ID: "machine", Name: "Machine", Public: false,
		RedirectURIs: []string{"https://client.example/callback"}, GrantTypes: []string{"client_credentials"},
		ResponseTypes: []string{"code"}, Scopes: []string{"kubeloop.api"}, Enabled: true,
		MachinePrincipalID: principal.ID}, "correct-secret")
	form := url.Values{"grant_type": {"client_credentials"}, "scope": {"kubeloop.api"}, "client_id": {"machine"}, "client_secret": {"correct-secret"}}
	request := httptest.NewRequest(http.MethodPost, "https://issuer.example/oauth2/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	endpoints.Token(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("token status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		AccessToken  string `json:"access_token"`
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.AccessToken == "" || strings.Count(response.AccessToken, ".") != 1 || response.IDToken != "" || response.RefreshToken != "" {
		t.Fatalf("unexpected token response %#v", response)
	}
	session, _, err := endpoints.IntrospectAccessToken(context.Background(), response.AccessToken)
	if err != nil || !session.Machine || session.PrincipalID != principal.ID {
		t.Fatalf("introspection = %#v, %v", session, err)
	}
}

func TestPasswordGrantUsesLocalAuthenticatorAndMFA(t *testing.T) {
	var factor string
	var expected controlstorage.Principal
	endpoints, store, principal := newTestEndpoints(t, func(_ context.Context, username string, password []byte, secondFactor, _, _, _ string) (controlstorage.Principal, error) {
		factor = secondFactor
		if username != "admin" || string(password) != "password" || secondFactor != "123456" {
			return controlstorage.Principal{}, fosite.ErrNotFound
		}
		return expected, nil
	})
	expected = principal
	createTestClient(t, store, controlstorage.OAuthClient{ID: "password-client", Name: "Password", Public: false,
		RedirectURIs: []string{"https://client.example/callback"}, GrantTypes: []string{"password", "refresh_token"},
		ResponseTypes: []string{"code"}, Scopes: []string{"kubeloop.api", "offline_access"}, Enabled: true}, "correct-secret")
	form := url.Values{"grant_type": {"password"}, "username": {"admin"}, "password": {"password"}, "second_factor": {"123456"}, "scope": {"kubeloop.api offline_access"}, "client_id": {"password-client"}, "client_secret": {"correct-secret"}}
	request := httptest.NewRequest(http.MethodPost, "https://issuer.example/oauth2/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	endpoints.Token(recorder, request)
	if recorder.Code != http.StatusOK || factor != "123456" {
		t.Fatalf("password status=%d factor=%q body=%s", recorder.Code, factor, recorder.Body.String())
	}
	var response map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &response)
	if response["access_token"] == nil || response["refresh_token"] == nil || response["id_token"] != nil {
		t.Fatalf("unexpected response %#v", response)
	}
}

func TestAuthorizationCodeRequiresPKCES256AndRejectsReplay(t *testing.T) {
	endpoints, store, principal := newTestEndpoints(t, nil)
	createTestClient(t, store, controlstorage.OAuthClient{ID: "public-client", Name: "Public", Public: true,
		RedirectURIs: []string{"https://client.example/callback"}, GrantTypes: []string{"authorization_code", "refresh_token"},
		ResponseTypes: []string{"code"}, Scopes: []string{"openid", "offline_access", "kubeloop.api"}, Trusted: true, Enabled: true}, "")
	verifier := strings.Repeat("v", 64)
	challengeSum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeSum[:])
	query := url.Values{"response_type": {"code"}, "client_id": {"public-client"}, "redirect_uri": {"https://client.example/callback"},
		"scope": {"openid offline_access kubeloop.api"}, "state": {strings.Repeat("s", 32)}, "nonce": {strings.Repeat("n", 32)},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "provider": {"local"}}
	authorize := httptest.NewRequest(http.MethodGet, "https://issuer.example/oauth2/authorize?"+query.Encode(), nil)
	transaction, err := endpoints.BeginAuthorization(authorize.Context(), authorize)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	if err := endpoints.CompleteAuthorization(recorder, authorize, transaction.Transaction, transaction.CSRF, principal, true); err != nil {
		t.Fatal(err)
	}
	redirect, err := url.Parse(recorder.Header().Get("Location"))
	if err != nil || redirect.Query().Get("code") == "" {
		t.Fatalf("authorize response status=%d location=%q body=%s err=%v", recorder.Code, recorder.Header().Get("Location"), recorder.Body.String(), err)
	}
	code := redirect.Query().Get("code")
	exchange := func(code string) *httptest.ResponseRecorder {
		form := url.Values{"grant_type": {"authorization_code"}, "client_id": {"public-client"}, "redirect_uri": {"https://client.example/callback"}, "code": {code}, "code_verifier": {verifier}}
		request := httptest.NewRequest(http.MethodPost, "https://issuer.example/oauth2/token", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		result := httptest.NewRecorder()
		endpoints.Token(result, request)
		return result
	}
	first := exchange(code)
	if first.Code != http.StatusOK {
		t.Fatalf("exchange status=%d body=%s", first.Code, first.Body.String())
	}
	var response map[string]any
	_ = json.Unmarshal(first.Body.Bytes(), &response)
	if response["access_token"] == nil || response["refresh_token"] == nil || response["id_token"] == nil {
		t.Fatalf("tokens=%#v", response)
	}
	if response["refresh_expires_in"] != nil {
		t.Fatalf("non-standard refresh expiry leaked into token response: %#v", response)
	}
	second := exchange(code)
	if second.Code == http.StatusOK {
		t.Fatalf("replayed code accepted: %s", second.Body.String())
	}
	missingQuery := url.Values{}
	for key, values := range query {
		missingQuery[key] = append([]string(nil), values...)
	}
	missingQuery.Del("code_challenge_method")
	missing := httptest.NewRequest(http.MethodGet, "https://issuer.example/oauth2/authorize?"+missingQuery.Encode(), nil)
	if _, err := endpoints.BeginAuthorization(missing.Context(), missing); err == nil {
		t.Fatal("authorization without PKCE method was accepted")
	}
}

func TestImplicitAndHybridResponses(t *testing.T) {
	endpoints, store, principal := newTestEndpoints(t, nil)
	createTestClient(t, store, controlstorage.OAuthClient{ID: "browser-client", Name: "Browser", Public: true,
		RedirectURIs: []string{"https://client.example/callback"}, GrantTypes: []string{"authorization_code", "implicit", "refresh_token"},
		ResponseTypes: []string{"token", "id_token", "id_token token", "code token", "code id_token", "code id_token token"},
		Scopes:        []string{"openid", "offline_access", "kubeloop.api"}, Trusted: true, Enabled: true}, "")
	verifier := strings.Repeat("p", 64)
	challengeSum := sha256.Sum256([]byte(verifier))
	for _, responseType := range []string{"token", "id_token", "id_token token", "code token", "code id_token", "code id_token token"} {
		t.Run(responseType, func(t *testing.T) {
			responseParts := strings.Fields(responseType)
			query := url.Values{"response_type": {responseType}, "client_id": {"browser-client"}, "redirect_uri": {"https://client.example/callback"},
				"scope": {"openid offline_access kubeloop.api"}, "state": {strings.Repeat("s", 32)}, "nonce": {strings.Repeat("n", 32)}}
			if slices.Contains(responseParts, "code") {
				query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challengeSum[:]))
				query.Set("code_challenge_method", "S256")
			}
			request := httptest.NewRequest(http.MethodGet, "https://issuer.example/oauth2/authorize?"+query.Encode(), nil)
			challenge, err := endpoints.BeginAuthorization(request.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			if err := endpoints.CompleteAuthorization(recorder, request, challenge.Transaction, challenge.CSRF, principal, true); err != nil {
				t.Fatal(err)
			}
			redirect, err := url.Parse(recorder.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			fragment, err := url.ParseQuery(redirect.Fragment)
			if err != nil || fragment.Get("state") == "" {
				t.Fatalf("fragment=%q err=%v", redirect.Fragment, err)
			}
			if slices.Contains(responseParts, "token") && fragment.Get("access_token") == "" {
				t.Fatalf("access token missing from %q", redirect.Fragment)
			}
			if slices.Contains(responseParts, "id_token") && fragment.Get("id_token") == "" {
				t.Fatalf("ID token missing from %q", redirect.Fragment)
			}
			if slices.Contains(responseParts, "code") && fragment.Get("code") == "" {
				t.Fatalf("code missing from %q", redirect.Fragment)
			}
			if fragment.Get("refresh_token") != "" {
				t.Fatal("front-channel response leaked a refresh token")
			}
		})
	}
}

func TestRefreshRotationReplayRevokesEntireGrant(t *testing.T) {
	var principal controlstorage.Principal
	endpoints, store, seeded := newTestEndpoints(t, func(_ context.Context, username string, password []byte, _, _, _, _ string) (controlstorage.Principal, error) {
		if username != "admin" || string(password) != "password" {
			return controlstorage.Principal{}, fosite.ErrNotFound
		}
		return principal, nil
	})
	principal = seeded
	createTestClient(t, store, controlstorage.OAuthClient{ID: "rotation", Name: "Rotation", Public: false,
		RedirectURIs: []string{"https://client.example/callback"}, GrantTypes: []string{"password", "refresh_token"},
		ResponseTypes: []string{"code"}, Scopes: []string{"kubeloop.api", "offline_access"}, Enabled: true}, "secret")
	issue := func(values url.Values) (int, map[string]any) {
		request := httptest.NewRequest(http.MethodPost, "https://issuer.example/oauth2/token", strings.NewReader(values.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		recorder := httptest.NewRecorder()
		endpoints.Token(recorder, request)
		var response map[string]any
		_ = json.Unmarshal(recorder.Body.Bytes(), &response)
		return recorder.Code, response
	}
	status, first := issue(url.Values{"grant_type": {"password"}, "username": {"admin"}, "password": {"password"},
		"scope": {"kubeloop.api offline_access"}, "client_id": {"rotation"}, "client_secret": {"secret"}})
	if status != http.StatusOK {
		t.Fatalf("initial token status=%d response=%#v", status, first)
	}
	oldRefresh := first["refresh_token"].(string)
	status, rotated := issue(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {oldRefresh}, "client_id": {"rotation"}, "client_secret": {"secret"}})
	if status != http.StatusOK || rotated["refresh_token"] == nil {
		t.Fatalf("rotation status=%d response=%#v", status, rotated)
	}
	status, _ = issue(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {oldRefresh}, "client_id": {"rotation"}, "client_secret": {"secret"}})
	if status == http.StatusOK {
		t.Fatal("replayed refresh token was accepted")
	}
	status, _ = issue(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rotated["refresh_token"].(string)}, "client_id": {"rotation"}, "client_secret": {"secret"}})
	if status == http.StatusOK {
		t.Fatal("grant remained active after refresh token replay")
	}
}

func TestBrowserSessionRevocationInvalidatesIdentity(t *testing.T) {
	endpoints, _, principal := newTestEndpoints(t, nil)
	token, err := endpoints.CreateBrowserSession(t.Context(), principal, time.Hour)
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

func newTestEndpoints(t *testing.T, authenticate PasswordAuthenticator) (*Endpoints, *controlstorage.Store, controlstorage.Principal) {
	t.Helper()
	store, err := controlstorage.Open(t.Context(), controlstorage.Config{Backend: controlstorage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "oauth.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	principal, err := store.Principals().Upsert(t.Context(), controlstorage.Principal{ID: uuid.NewString(), Provider: "local", ExternalID: "admin", DisplayName: "Admin", Email: "admin@example.test", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fositeStorage, err := NewStorage(store, authenticate)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewProvider(fositeStorage, Config{Issuer: "https://issuer.example", HMACSecret: []byte("01234567890123456789012345678901"), SigningKey: key, AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	endpoints, err := NewEndpoints(provider, store, "test-key", key, fositeStorage)
	if err != nil {
		t.Fatal(err)
	}
	return endpoints, store, principal
}

func createTestClient(t *testing.T, store *controlstorage.Store, client controlstorage.OAuthClient, secret string) {
	t.Helper()
	now := time.Now().UTC()
	client.CreatedAt = now
	client.UpdatedAt = now
	if err := store.OAuthClients().Create(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	if secret != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.OAuthClients().SetSecret(t.Context(), controlstorage.OAuthClientSecret{ClientID: client.ID, SecretHash: hash, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
}
