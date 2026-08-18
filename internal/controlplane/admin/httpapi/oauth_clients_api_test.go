package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func TestOAuthClientCRUDAndSecretRotation(t *testing.T) {
	handler, store := newReadTestHandler(t, WithOAuthClients(nilSafeRepositories(t), nilSafeTransactions(t)))
	// Replace the helpers' temporary stores with the handler's actual store.
	handler.readAPI.oauthRepositories = store
	handler.readAPI.oauthTransactions = store
	cookie, csrf := issueTestSession(t, handler)

	created := oauthClientWrite(t, handler, cookie, csrf, http.MethodPost, "/oauth-clients", map[string]any{
		"id": "automation", "name": "Automation", "public": false, "redirectUris": []string{"https://client.example/callback"},
		"grantTypes": []string{"client_credentials"}, "scopes": []string{"kubeloop.api"}, "enabled": true,
		"reason": "create automation client",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var document map[string]any
	if json.Unmarshal(created.Body.Bytes(), &document) != nil || document["clientSecret"] == "" || document["machineIdentityId"] == "" {
		t.Fatalf("create body=%s", created.Body.String())
	}
	if _, err := store.OAuthClients().GetSecret(t.Context(), "automation"); err != nil {
		t.Fatal(err)
	}

	rotated := oauthClientWrite(t, handler, cookie, csrf, http.MethodPost, "/oauth-clients/automation/secret", map[string]any{"reason": "rotate automation secret"})
	if rotated.Code != http.StatusOK || !bytes.Contains(rotated.Body.Bytes(), []byte("clientSecret")) {
		t.Fatalf("rotate status=%d body=%s", rotated.Code, rotated.Body.String())
	}

	invalid := oauthClientWrite(t, handler, cookie, csrf, http.MethodPost, "/oauth-clients", map[string]any{
		"id": "unsafe", "name": "Unsafe", "public": true, "redirectUris": []string{"https://client.example/callback"},
		"grantTypes": []string{"password"}, "scopes": []string{"kubeloop.api"}, "enabled": true,
		"reason": "reject unsupported grant",
	})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("unsafe public password client status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}

func TestOAuthClientRedirectURIValidation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		clientID string
		redirect string
		valid    bool
	}{
		{name: "https", clientID: "client", redirect: "https://client.example/callback", valid: true},
		{name: "IPv4 loopback", clientID: "client", redirect: "http://127.0.0.1:8080/callback", valid: true},
		{name: "IPv6 loopback", clientID: "client", redirect: "http://[::1]:8080/callback", valid: true},
		{name: "desktop protocol", clientID: storage.DesktopOAuthClientID, redirect: storage.DesktopOAuthRedirectURI, valid: true},
		{name: "desktop protocol on another client", clientID: "client", redirect: storage.DesktopOAuthRedirectURI},
		{name: "other custom protocol", clientID: "client", redirect: "other://auth/callback"},
		{name: "non-loopback HTTP", clientID: "client", redirect: "http://client.example/callback"},
		{name: "userinfo", clientID: "client", redirect: "https://user@client.example/callback"},
		{name: "fragment", clientID: "client", redirect: "https://client.example/callback#token"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := oauthClientFromInput(oauthClientInput{
				ID: test.clientID, Name: "Client", Public: true,
				RedirectURIs: []string{test.redirect}, GrantTypes: []string{"authorization_code"},
				Scopes: []string{"openid"}, Enabled: true,
			})
			if (err == nil) != test.valid {
				t.Fatalf("oauthClientFromInput() error = %v, valid = %t", err, test.valid)
			}
		})
	}
}

// WithOAuthClients validates interfaces before the real store is returned by
// newIdentityTokenHandler; these lightweight stores are used only to satisfy
// construction and are immediately replaced above.
func nilSafeRepositories(t *testing.T) storage.Repositories {
	t.Helper()
	store, err := storage.Open(t.Context(), storage.Config{Backend: storage.BackendSQLite, SQLitePath: t.TempDir() + "/oauth-options.db"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
func nilSafeTransactions(t *testing.T) storage.TransactionManager {
	return nilSafeRepositories(t).(storage.TransactionManager)
}

func oauthClientWrite(t *testing.T, handler *Handler, cookie *http.Cookie, csrf, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(raw))
	request.AddCookie(cookie)
	request.Header.Set("Origin", "https://gateway.example")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(CSRFHeaderName, csrf)
	recorder := httptest.NewRecorder()
	serveHTTP(handler, recorder, request)
	return recorder
}
