package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
)

func TestOIDCLoopbackLoginUsesStatePKCEAndExchange(t *testing.T) {
	var server *httptest.Server
	var challenge string
	browserCallbacks := 0
	exchangeCode := strings.Repeat("c", 43)
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			writeProviderMetadata(t, writer, server.URL)
		case "/oauth2/token":
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256([]byte(request.Form.Get("code_verifier")))
			if request.Form.Get("code") != exchangeCode || base64.RawURLEncoding.EncodeToString(hash[:]) != challenge ||
				request.Form.Get("device_id") != "device-1" || request.Form.Get("client_id") != DefaultClientID {
				t.Fatalf("exchange form = %#v", request.Form)
			}
			writeTokenResponse(t, writer)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := New(Config{
		HTTPClient:      server.Client(),
		BrowserCallback: func() { browserCallbacks++ },
		OpenBrowser: func(target string) error {
			authorize, err := url.Parse(target)
			if err != nil {
				return err
			}
			query := authorize.Query()
			challenge = query.Get("code_challenge")
			if authorize.Path != "/oauth2/authorize" || query.Get("provider") != "company" ||
				query.Get("client_id") != DefaultClientID || len(query.Get("state")) < 32 ||
				len(query.Get("nonce")) < 32 || len(challenge) != 43 || query.Get("code_challenge_method") != "S256" {
				t.Fatalf("authorization URL = %q", target)
			}
			callback, err := url.Parse(query.Get("redirect_uri"))
			if err != nil {
				return err
			}
			callbackQuery := callback.Query()
			callbackQuery.Set("state", authorize.Query().Get("state"))
			callbackQuery.Set("code", exchangeCode)
			callback.RawQuery = callbackQuery.Encode()
			response, err := http.Get(callback.String())
			if err != nil {
				return err
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				raw, _ := io.ReadAll(response.Body)
				t.Fatalf("callback status = %d: %s", response.StatusCode, raw)
			}
			return nil
		},
	})
	credential, err := client.LoginOIDC(context.Background(), server.URL, "company", "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccessToken != "access-token" || credential.RefreshToken != "refresh-token" || credential.DeviceID != "device-1" ||
		credential.IdentityID != "identity-1" || credential.UserName != "Example User" {
		t.Fatalf("credential = %#v", credential)
	}
	if !credential.RefreshExpiresAt.IsZero() {
		t.Fatalf("standard OAuth response inferred a refresh expiry: %s", credential.RefreshExpiresAt)
	}
	if browserCallbacks != 1 {
		t.Fatalf("browser callbacks = %d, want 1", browserCallbacks)
	}
}

func TestLoopbackCallbackRejectsTamperedStateWithoutConsumingLogin(t *testing.T) {
	results := make(chan callbackResult, 1)
	server := httptest.NewServer(newLoopbackServer(strings.Repeat("s", 43), results).Handler)
	defer server.Close()
	response, err := http.Get(server.URL + "/callback?state=" + strings.Repeat("x", 43) + "&code=" + strings.Repeat("c", 43))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("tampered status = %d", response.StatusCode)
	}
	select {
	case result := <-results:
		t.Fatalf("tampered callback produced result: %#v", result)
	default:
	}
	response, err = http.Get(server.URL + "/callback?state=" + strings.Repeat("s", 43) + "&code=" + strings.Repeat("c", 43))
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK ||
		!strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") ||
		!strings.Contains(response.Header.Get("Content-Security-Policy"), "script-src 'sha256-") ||
		!strings.Contains(string(body), "window.close();") {
		t.Fatalf("valid callback response status=%d headers=%v body=%s", response.StatusCode, response.Header, body)
	}
	if result := <-results; result.err != nil || result.code == "" {
		t.Fatalf("valid callback result = %#v", result)
	}
}

func TestRefreshRevokeAndUnsafeTargets(t *testing.T) {
	requests := 0
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			writeProviderMetadata(t, writer, server.URL)
		case "/oauth2/token":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"token_type": "Bearer", "access_token": "access-token", "refresh_token": "refresh-token",
				"expires_in": 60,
			})
		case "/oauth2/revoke":
			writer.WriteHeader(http.StatusOK)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := New(Config{HTTPClient: server.Client()})
	current := credentialForTest()
	refreshed, err := client.Refresh(context.Background(), server.URL, current)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.IdentityID != current.IdentityID || refreshed.UserName != current.UserName {
		t.Fatalf("refreshed identity = %#v", refreshed)
	}
	if err := client.Revoke(context.Background(), server.URL, current.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if requests != 4 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestAuthenticationRejectionReturnsTypedAPIError(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/.well-known/openid-configuration" {
			writeProviderMetadata(t, writer, server.URL)
			return
		}
		writer.Header().Set("X-Request-ID", "request-123")
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"error":"invalid_grant","error_description":"credentials were rejected"}`))
	}))
	defer server.Close()
	_, err := New(Config{HTTPClient: server.Client()}).Refresh(
		context.Background(), server.URL, credentialForTest(),
	)
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.Status != http.StatusUnauthorized ||
		apiError.Code != CodeInvalidGrant || apiError.RequestID != "request-123" {
		t.Fatalf("authentication error = %#v, %v", apiError, err)
	}
}

func writeTokenResponse(t *testing.T, writer http.ResponseWriter) {
	t.Helper()
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"token_type": "Bearer", "access_token": "access-token", "refresh_token": "refresh-token",
		"expires_in": 60, "id_token": testIDToken(),
	})
}

func writeProviderMetadata(t *testing.T, writer http.ResponseWriter, issuer string) {
	t.Helper()
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"issuer": issuer, "authorization_endpoint": issuer + "/oauth2/authorize",
		"token_endpoint": issuer + "/oauth2/token", "revocation_endpoint": issuer + "/oauth2/revoke",
	})
}

func credentialForTest() credentials.Credential {
	return credentials.Credential{
		TokenType: "Bearer", AccessToken: "old-access", RefreshToken: "old-refresh", DeviceID: "device-1",
		AccessExpiresAt: time.Now().Add(time.Minute), RefreshExpiresAt: time.Now().Add(time.Hour),
		IdentityID: "identity-1", UserName: "Example User",
	}
}

func testIDToken() string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"identity-1","name":"Example User","email":"user@example.test"}`))
	return "header." + payload + ".signature"
}
