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
	var callbackURL, state, challenge string
	exchangeCode := strings.Repeat("c", 43)
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/auth/oidc/company/start":
			var body struct {
				ClientCallback string `json:"clientCallback"`
				State          string `json:"state"`
				Nonce          string `json:"nonce"`
				PKCEChallenge  string `json:"pkceChallenge"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			callbackURL, state, challenge = body.ClientCallback, body.State, body.PKCEChallenge
			if !strings.HasPrefix(callbackURL, "http://127.0.0.1:") || len(state) < 32 || len(body.Nonce) < 32 || len(challenge) != 43 {
				t.Fatalf("start body = %#v", body)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"authorizationUrl": server.URL + "/idp", "expiresAt": time.Now().Add(time.Minute),
			})
		case "/auth/token/exchange":
			var body struct {
				Code         string `json:"code"`
				PKCEVerifier string `json:"pkceVerifier"`
				DeviceID     string `json:"deviceId"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256([]byte(body.PKCEVerifier))
			if body.Code != exchangeCode || base64.RawURLEncoding.EncodeToString(hash[:]) != challenge || body.DeviceID != "device-1" {
				t.Fatalf("exchange body = %#v", body)
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
			if target != server.URL+"/idp" {
				t.Fatalf("authorization URL = %q", target)
			}
			callback, err := url.Parse(callbackURL)
			if err != nil {
				return err
			}
			query := callback.Query()
			query.Set("state", state)
			query.Set("code", exchangeCode)
			callback.RawQuery = query.Encode()
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

func TestADLoginClearsPasswordAndUsesOnlySelectedOrigin(t *testing.T) {
	password := []byte("correct horse battery staple")
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/auth/ad/corp/login" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["username"] != "ada" || body["password"] != "correct horse battery staple" || body["deviceId"] != "device-1" {
			t.Fatalf("body = %#v", body)
		}
		writeTokenResponse(t, writer)
	}))
	defer server.Close()
	credential, err := New(Config{HTTPClient: server.Client()}).LoginAD(
		context.Background(), server.URL, "corp", "ada", password, "device-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccessToken != "access-token" {
		t.Fatalf("credential = %#v", credential)
	}
	for _, value := range password {
		if value != 0 {
			t.Fatalf("password was not cleared: %v", password)
		}
	}
}

func TestDevelopmentLoginsUseSelectedOriginAndClearStaticToken(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch request.URL.Path {
		case "/auth/static-token/local/login":
			if body["token"] != "0123456789abcdef0123456789abcdef" || body["deviceId"] != "device-static" {
				t.Fatalf("static-token body = %#v", body)
			}
		case "/auth/anonymous/guest/login":
			if len(body) != 1 || body["deviceId"] != "device-anonymous" {
				t.Fatalf("anonymous body = %#v", body)
			}
		default:
			t.Fatalf("path = %q", request.URL.Path)
		}
		writeTokenResponse(t, writer)
	}))
	defer server.Close()
	client := New(Config{HTTPClient: server.Client()})
	presented := []byte("0123456789abcdef0123456789abcdef")
	staticCredential, err := client.LoginStaticToken(
		context.Background(), server.URL, "local", presented, "device-static",
	)
	if err != nil || staticCredential.AccessToken != "access-token" {
		t.Fatalf("static-token credential = %#v, %v", staticCredential, err)
	}
	for _, value := range presented {
		if value != 0 {
			t.Fatal("static token was not cleared")
		}
	}
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
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		switch request.URL.Path {
		case "/auth/token/refresh":
			writeTokenResponse(t, writer)
		case "/auth/token/revoke":
			writer.WriteHeader(http.StatusNoContent)
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
	if requests != 2 {
		t.Fatalf("requests = %d", requests)
	}
	if _, err := client.LoginAD(context.Background(), "http://remote.example.test", "corp", "ada", []byte("secret"), "device"); err == nil {
		t.Fatal("plain HTTP remote AD login was accepted")
	}
	if _, err := client.LoginAD(context.Background(), server.URL, "../corp", "ada", []byte("secret"), "device"); err == nil {
		t.Fatal("unsafe provider ID was accepted")
	}
}

func TestAuthenticationRejectionReturnsTypedAPIError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-Request-ID", "request-123")
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"code":"INVALID_CREDENTIALS","message":"credentials were rejected"}`))
	}))
	defer server.Close()
	password := []byte("wrong-password")
	_, err := New(Config{HTTPClient: server.Client()}).LoginAD(
		context.Background(), server.URL, "corp", "ada", password, "device-1",
	)
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.Status != http.StatusUnauthorized ||
		apiError.Code != CodeInvalidCredentials || apiError.RequestID != "request-123" {
		t.Fatalf("authentication error = %#v, %v", apiError, err)
	}
	for _, value := range password {
		if value != 0 {
			t.Fatal("password was not cleared after typed rejection")
		}
	}
}

func writeTokenResponse(t *testing.T, writer http.ResponseWriter) {
	t.Helper()
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"tokenType": "Bearer", "accessToken": "access-token", "refreshToken": "refresh-token",
		"accessExpiresAt": time.Now().Add(time.Minute), "refreshExpiresAt": time.Now().Add(time.Hour),
	})
}

func credentialForTest() credentials.Credential {
	return credentials.Credential{
		TokenType: "Bearer", AccessToken: "old-access", RefreshToken: "old-refresh", DeviceID: "device-1",
		AccessExpiresAt: time.Now().Add(time.Minute), RefreshExpiresAt: time.Now().Add(time.Hour),
	}
}
