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
		HTTPClient: server.Client(),
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
	if credential.AccessToken != "access-token" || credential.RefreshToken != "refresh-token" || credential.DeviceID != "device-1" {
		t.Fatalf("credential = %#v", credential)
	}
}

func TestAnonymousLoginUsesSelectedOrigin(t *testing.T) {
	requests := 0
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.Path == "/.well-known/openid-configuration" {
			writeProviderMetadata(t, writer, server.URL)
			return
		}
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		switch request.URL.Path {
		case "/oauth2/token":
			if request.Form.Get("grant_type") != "urn:kubeloop:params:oauth:grant-type:anonymous" ||
				request.Form.Get("provider") != "guest" || request.Form.Get("device_id") != "device-anonymous" {
				t.Fatalf("anonymous form = %#v", request.Form)
			}
		default:
			t.Fatalf("path = %q", request.URL.Path)
		}
		writeTokenResponse(t, writer)
	}))
	defer server.Close()
	client := New(Config{HTTPClient: server.Client()})
	anonymousCredential, err := client.LoginAnonymous(
		context.Background(), server.URL, "guest", "device-anonymous",
	)
	if err != nil || anonymousCredential.RefreshToken != "refresh-token" {
		t.Fatalf("anonymous credential = %#v, %v", anonymousCredential, err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d", requests)
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
	response.Body.Close()
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
			writeTokenResponse(t, writer)
		case "/oauth2/revoke":
			writer.WriteHeader(http.StatusOK)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := New(Config{HTTPClient: server.Client()})
	current := credentialForTest()
	if _, err := client.Refresh(context.Background(), server.URL, current); err != nil {
		t.Fatal(err)
	}
	if err := client.Revoke(context.Background(), server.URL, current.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if requests != 4 {
		t.Fatalf("requests = %d", requests)
	}
	if _, err := client.LoginAnonymous(context.Background(), server.URL, "../corp", "device"); err == nil {
		t.Fatal("unsafe provider ID was accepted")
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
	_, err := New(Config{HTTPClient: server.Client()}).LoginAnonymous(
		context.Background(), server.URL, "guest", "device-1",
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
		"expires_in": 60, "refresh_expires_in": 3600,
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
	}
}
