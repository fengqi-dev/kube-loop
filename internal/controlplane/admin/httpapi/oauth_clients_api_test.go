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
	handler, store := newPrincipalTokenHandler(t, false, WithOAuthClients(nilSafeRepositories(t), nilSafeTransactions(t)))
	// Replace the helpers' temporary stores with the handler's actual store.
	handler.readAPI.oauthRepositories = store
	handler.readAPI.oauthTransactions = store
	cookie, csrf := exchangePrincipalSession(t, handler)

	created := oauthClientWrite(t, handler, cookie, csrf, http.MethodPost, "/oauth-clients", map[string]any{
		"id": "automation", "name": "Automation", "public": false, "redirectUris": []string{"https://client.example/callback"},
		"grantTypes": []string{"client_credentials"}, "responseTypes": []string{"code"}, "scopes": []string{"kubeloop.api"}, "enabled": true,
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var document map[string]any
	if json.Unmarshal(created.Body.Bytes(), &document) != nil || document["clientSecret"] == "" || document["machinePrincipalId"] == "" {
		t.Fatalf("create body=%s", created.Body.String())
	}
	if _, err := store.OAuthClients().GetSecret(t.Context(), "automation"); err != nil {
		t.Fatal(err)
	}

	rotated := oauthClientWrite(t, handler, cookie, csrf, http.MethodPost, "/oauth-clients/automation/secret", map[string]any{})
	if rotated.Code != http.StatusOK || !bytes.Contains(rotated.Body.Bytes(), []byte("clientSecret")) {
		t.Fatalf("rotate status=%d body=%s", rotated.Code, rotated.Body.String())
	}

	invalid := oauthClientWrite(t, handler, cookie, csrf, http.MethodPost, "/oauth-clients", map[string]any{
		"id": "unsafe", "name": "Unsafe", "public": true, "redirectUris": []string{"https://client.example/callback"},
		"grantTypes": []string{"password"}, "responseTypes": []string{"code"}, "scopes": []string{"kubeloop.api"}, "enabled": true,
	})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("unsafe public password client status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}

// WithOAuthClients validates interfaces before the real store is returned by
// newPrincipalTokenHandler; these lightweight stores are used only to satisfy
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

func exchangePrincipalSession(t *testing.T, handler *Handler) (*http.Cookie, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/sessions/token", bytes.NewBufferString(`{}`))
	request.RemoteAddr = "192.0.2.30:5000"
	request.Header.Set("Origin", "https://gateway.example")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer valid-access-token")
	recorder := httptest.NewRecorder()
	serveHTTP(handler, recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("exchange status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		CSRFToken string `json:"csrfToken"`
	}
	if json.Unmarshal(recorder.Body.Bytes(), &response) != nil {
		t.Fatal("decode exchange")
	}
	return recorder.Result().Cookies()[0], response.CSRFToken
}
