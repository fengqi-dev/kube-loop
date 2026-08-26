package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/auth"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func TestOAuthClientCRUDAndSecretRotation(t *testing.T) {
	handler, store := newReadTestHandler(
		t,
		WithOAuthClients(nilSafeRepositories(t), nilSafeTransactions(t)),
	)
	// Replace the helpers' temporary stores with the handler's actual store.
	handler.readAPI.oauthRepositories = store
	handler.readAPI.oauthTransactions = store
	cookie, csrf := issueTestSession(t, handler)

	created := oauthClientWrite(
		t,
		handler,
		cookie,
		csrf,
		http.MethodPost,
		"/oauth-clients",
		map[string]any{
			"id":              "automation",
			nameField:         "Automation",
			publicField:       false,
			redirectURIsField: []string{"https://client.example/callback"},
			grantTypesField: []string{
				"client_credentials",
			},
			scopesField:  []string{"kubeloop.api"},
			enabledField: true,
			"reason":     "create automation client",
		},
	)
	if created.Code != http.StatusCreated {
		t.Fatalf(
			"create status=%d body=%s",
			created.Code,
			created.Body.String(),
		)
	}
	var document map[string]any
	if json.Unmarshal(created.Body.Bytes(), &document) != nil ||
		document["clientSecret"] == "" ||
		document["machineIdentityId"] == "" {
		t.Fatalf("create body=%s", created.Body.String())
	}
	if _, err := store.OAuthClients().GetSecret(t.Context(), "automation"); err != nil {
		t.Fatal(err)
	}

	listed := oauthClientRequest(
		t, handler, cookie, csrf, http.MethodGet, "/oauth-clients", nil, nil,
	)
	if listed.Code != http.StatusOK || !bytes.Contains(listed.Body.Bytes(), []byte(`"automation"`)) {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}

	stored, err := store.OAuthClients().Get(t.Context(), "automation")
	if err != nil {
		t.Fatal(err)
	}
	updated := oauthClientRequest(
		t,
		handler,
		cookie,
		csrf,
		http.MethodPut,
		"/oauth-clients/automation",
		map[string]any{
			nameField:         "Updated Automation",
			publicField:       false,
			redirectURIsField: []string{"https://client.example/callback"},
			grantTypesField:   []string{"client_credentials"},
			scopesField:       []string{"kubeloop.api"},
			enabledField:      true,
			"reason":          "update automation client",
		},
		map[string]string{"If-Match": iamETag(stored.UpdatedAt)},
	)
	if updated.Code != http.StatusOK ||
		!bytes.Contains(updated.Body.Bytes(), []byte(`"Updated Automation"`)) ||
		updated.Header().Get("ETag") == "" {
		t.Fatalf("update status=%d headers=%v body=%s", updated.Code, updated.Header(), updated.Body.String())
	}

	stored, err = store.OAuthClients().Get(t.Context(), "automation")
	if err != nil {
		t.Fatal(err)
	}
	disabled := oauthClientRequest(
		t,
		handler,
		cookie,
		csrf,
		http.MethodPost,
		"/oauth-clients/automation/enabled",
		map[string]any{"enabled": false, "reason": "disable automation client"},
		map[string]string{"If-Match": iamETag(stored.UpdatedAt)},
	)
	if disabled.Code != http.StatusOK ||
		!bytes.Contains(disabled.Body.Bytes(), []byte(`"enabled":false`)) ||
		disabled.Header().Get("ETag") == "" {
		t.Fatalf("disable status=%d headers=%v body=%s", disabled.Code, disabled.Header(), disabled.Body.String())
	}

	rotated := oauthClientWrite(
		t,
		handler,
		cookie,
		csrf,
		http.MethodPost,
		"/oauth-clients/automation/secret",
		map[string]any{"reason": "rotate automation secret"},
	)
	if rotated.Code != http.StatusOK ||
		!bytes.Contains(rotated.Body.Bytes(), []byte("clientSecret")) {
		t.Fatalf(
			"rotate status=%d body=%s",
			rotated.Code,
			rotated.Body.String(),
		)
	}

	stored, err = store.OAuthClients().Get(t.Context(), "automation")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	scopeHash := bytes.Repeat([]byte{7}, 32)
	if err := store.OAuthConsents().Grant(t.Context(), storage.OAuthConsent{
		IdentityID: stored.MachineIdentityID,
		ClientID:   stored.ID,
		ScopeHash:  scopeHash,
		Scopes:     []string{"kubeloop.api"},
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatal(err)
	}
	revoked := oauthClientRequest(
		t,
		handler,
		cookie,
		csrf,
		http.MethodDelete,
		"/oauth-clients/automation/consents/"+stored.MachineIdentityID,
		nil,
		map[string]string{"X-Kubeloop-Reason": "revoke automation consent"},
	)
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke consent status=%d body=%s", revoked.Code, revoked.Body.String())
	}
	if has, err := store.OAuthConsents().Has(
		t.Context(), stored.MachineIdentityID, stored.ID, scopeHash,
	); err != nil || has {
		t.Fatalf("revoked consent has=%t error=%v", has, err)
	}

	deleted := oauthClientRequest(
		t,
		handler,
		cookie,
		csrf,
		http.MethodDelete,
		"/oauth-clients/automation",
		nil,
		map[string]string{"X-Kubeloop-Reason": "delete automation client"},
	)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	if _, err := store.OAuthClients().Get(t.Context(), "automation"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("deleted OAuth client error=%v", err)
	}

	invalid := oauthClientWrite(
		t,
		handler,
		cookie,
		csrf,
		http.MethodPost,
		"/oauth-clients",
		map[string]any{
			"id":              "unsafe",
			nameField:         "Unsafe",
			publicField:       true,
			redirectURIsField: []string{"https://client.example/callback"},
			grantTypesField:   []string{"password"},
			scopesField:       []string{"kubeloop.api"},
			enabledField:      true,
			"reason":          "reject unsupported grant",
		},
	)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf(
			"unsafe public password client status=%d body=%s",
			invalid.Code,
			invalid.Body.String(),
		)
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
		{name: schemeHTTPS, clientID: "client", redirect: "https://client.example/callback", valid: true},
		{name: "IPv4 loopback", clientID: "client", redirect: "http://127.0.0.1:8080/callback", valid: true},
		{name: "IPv6 loopback", clientID: "client", redirect: "http://[::1]:8080/callback", valid: true},
		{name: "desktop protocol", clientID: auth.DesktopClientID, redirect: auth.DesktopRedirectURI, valid: true},
		{name: "desktop protocol on another client", clientID: "client", redirect: auth.DesktopRedirectURI},
		{name: "other custom protocol", clientID: "client", redirect: "other://auth/callback"},
		{name: "non-loopback HTTP", clientID: "client", redirect: "http://client.example/callback"},
		{name: "userinfo", clientID: "client", redirect: "https://user@client.example/callback"},
		{name: "fragment", clientID: "client", redirect: "https://client.example/callback#token"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := oauthClientFromInput(oauthClientInput{
				ID: test.clientID, Name: "Client", Public: true,
				RedirectURIs: []string{
					test.redirect,
				}, GrantTypes: []string{"authorization_code"},
				Scopes: []string{"openid"}, Enabled: true,
			})
			if (err == nil) != test.valid {
				t.Fatalf(
					"oauthClientFromInput() error = %v, valid = %t",
					err,
					test.valid,
				)
			}
		})
	}
}

// WithOAuthClients validates interfaces before the real store is returned by
// newIdentityTokenHandler; these lightweight stores are used only to satisfy
// construction and are immediately replaced above.
func nilSafeRepositories(t *testing.T) storage.Repositories {
	t.Helper()
	store, err := storage.Open(
		t.Context(),
		storage.Config{
			Backend:    storage.BackendSQLite,
			SQLitePath: t.TempDir() + "/oauth-options.db",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
func nilSafeTransactions(t *testing.T) storage.TransactionManager {
	return nilSafeRepositories(t).(storage.TransactionManager)
}

func oauthClientWrite(
	t *testing.T,
	handler *Handler,
	cookie *http.Cookie,
	csrf, method, path string,
	body any,
) *httptest.ResponseRecorder {
	t.Helper()
	return oauthClientRequest(t, handler, cookie, csrf, method, path, body, nil)
}

func oauthClientRequest(
	t *testing.T,
	handler *Handler,
	cookie *http.Cookie,
	csrf, method, path string,
	body any,
	headers map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(raw))
	request.AddCookie(cookie)
	request.Header.Set("Origin", "https://gateway.example")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set(CSRFHeaderName, csrf)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	serveHTTP(handler, recorder, request)
	return recorder
}
