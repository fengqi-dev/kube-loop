package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
)

func TestOIDCProtocolLoginUsesStatePKCEAndExchange(t *testing.T) {
	var server *httptest.Server
	var client *Client
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
				request.Form.Get("device_id") != "device-1" || request.Form.Get(authParamClientID) != DefaultClientID ||
				request.Form.Get("redirect_uri") != DefaultRedirectURI {
				t.Fatalf("exchange form = %#v", request.Form)
			}
			writeTokenResponse(t, writer)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client = New(Config{
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
				query.Get(authParamClientID) != DefaultClientID || len(query.Get("state")) < 32 ||
				query.Get("redirect_uri") != DefaultRedirectURI || len(query.Get("nonce")) < 32 ||
				len(challenge) != 43 || query.Get("code_challenge_method") != "S256" {
				t.Fatalf("authorization URL = %q", target)
			}
			callback, err := url.Parse(DefaultRedirectURI)
			if err != nil {
				return err
			}
			callbackQuery := callback.Query()
			callbackQuery.Set("state", authorize.Query().Get("state"))
			callbackQuery.Set("code", exchangeCode)
			callback.RawQuery = callbackQuery.Encode()
			return client.HandleCallbackURL(callback.String())
		},
	})
	credential, err := client.LoginOIDC(context.Background(), server.URL, "company", "device-1")
	if err != nil {
		t.Fatal(err)
	}
	tokenMismatch := credential.AccessToken != "access-token" || credential.RefreshToken != "refresh-token"
	identityMismatch := credential.IdentityID != "identity-1" || credential.UserName != "Example User"
	if tokenMismatch || identityMismatch || credential.DeviceID != "device-1" {
		t.Fatalf("credential = %#v", credential)
	}
	if !credential.RefreshExpiresAt.IsZero() {
		t.Fatalf("standard OAuth response inferred a refresh expiry: %s", credential.RefreshExpiresAt)
	}
	if browserCallbacks != 1 {
		t.Fatalf("browser callbacks = %d, want 1", browserCallbacks)
	}
}

func TestOIDCLoopbackLoginUsesEphemeralCallbackServer(t *testing.T) {
	var server *httptest.Server
	var redirectURI string
	exchangeCode := strings.Repeat("c", 43)
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			writeProviderMetadata(t, writer, server.URL)
		case "/oauth2/token":
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if request.Form.Get("code") != exchangeCode || request.Form.Get("redirect_uri") != redirectURI {
				t.Fatalf("exchange form = %#v", request.Form)
			}
			writeTokenResponse(t, writer)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := New(Config{
		HTTPClient:       server.Client(),
		RedirectURI:      "http://127.0.0.1/callback",
		LoopbackCallback: true,
		OpenBrowser: func(target string) error {
			authorize, err := url.Parse(target)
			if err != nil {
				return err
			}
			redirectURI = authorize.Query().Get("redirect_uri")
			callback, err := url.Parse(redirectURI)
			if err != nil {
				return err
			}
			query := callback.Query()
			query.Set("state", authorize.Query().Get("state"))
			query.Set("code", exchangeCode)
			callback.RawQuery = query.Encode()
			response, err := http.Get(callback.String())
			if err != nil {
				return err
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				return err
			}
			if response.StatusCode != http.StatusOK ||
				response.Header.Get("Cache-Control") != "no-store" ||
				!strings.Contains(string(body), "Login complete") {
				t.Fatalf("callback status = %d headers = %#v body = %q", response.StatusCode, response.Header, body)
			}
			return nil
		},
	})
	credential, err := client.LoginOIDC(context.Background(), server.URL, "company", "device-1")
	if err != nil {
		t.Fatal(err)
	}
	callback, err := url.Parse(redirectURI)
	if err != nil || callback.Hostname() != "127.0.0.1" || callback.Port() == "" ||
		callback.Path != "/callback" {
		t.Fatalf("loopback redirect URI = %q err = %v", redirectURI, err)
	}
	if credential.AccessToken != "access-token" || credential.RefreshToken != "refresh-token" {
		t.Fatalf("credential = %#v", credential)
	}
}

func TestLoopbackCallbackStopIsIdempotent(t *testing.T) {
	client := New(Config{RedirectURI: "http://127.0.0.1/callback", LoopbackCallback: true})
	redirectURI, stop, err := client.startLoopbackCallback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stop()
	stop()
	callback, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp", callback.Host, 100*time.Millisecond)
	if err == nil {
		_ = connection.Close()
		t.Fatal("loopback callback listener remained open after stop")
	}
}

func TestProtocolCallbackRejectsTamperedStateWithoutConsumingLogin(t *testing.T) {
	client := New(Config{})
	pending, err := client.beginCallback(strings.Repeat("s", 43))
	if err != nil {
		t.Fatal(err)
	}
	defer client.endCallback(pending)
	if err := client.HandleCallbackURL(
		DefaultRedirectURI + "?state=" + strings.Repeat("x", 43) + "&code=" + strings.Repeat("c", 43),
	); err == nil {
		t.Fatal("tampered callback succeeded")
	}
	select {
	case result := <-pending.result:
		t.Fatalf("tampered callback produced result: %#v", result)
	default:
	}
	if err := client.HandleCallbackURL(
		DefaultRedirectURI + "?state=" + strings.Repeat("s", 43) + "&code=" + strings.Repeat("c", 43),
	); err != nil {
		t.Fatal(err)
	}
	if result := <-pending.result; result.err != nil || result.code == "" {
		t.Fatalf("valid callback result = %#v", result)
	}
	if err := client.HandleCallbackURL(
		DefaultRedirectURI + "?state=" + strings.Repeat("s", 43) + "&code=" + strings.Repeat("d", 43),
	); err == nil {
		t.Fatal("duplicate callback succeeded")
	}
}

func TestProtocolCallbackValidatesTargetAndParameters(t *testing.T) {
	client := New(Config{})
	pending, err := client.beginCallback(strings.Repeat("s", 43))
	if err != nil {
		t.Fatal(err)
	}
	defer client.endCallback(pending)

	for _, test := range []struct {
		name string
		url  string
	}{
		{name: "wrong scheme", url: "https://auth/callback?state=" + strings.Repeat("s", 43) + "&code=" + strings.Repeat("c", 43)},
		{name: "wrong host", url: "kubeloop://other/callback?state=" + strings.Repeat("s", 43) + "&code=" + strings.Repeat("c", 43)},
		{name: "wrong path", url: "kubeloop://auth/other?state=" + strings.Repeat("s", 43) + "&code=" + strings.Repeat("c", 43)},
		{name: "duplicate state", url: DefaultRedirectURI + "?state=a&state=b&code=" + strings.Repeat("c", 43)},
		{name: "short code", url: DefaultRedirectURI + "?state=" + strings.Repeat("s", 43) + "&code=short"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := client.HandleCallbackURL(test.url); err == nil {
				t.Fatal("invalid callback succeeded")
			}
		})
	}
	select {
	case result := <-pending.result:
		t.Fatalf("invalid callback produced result: %#v", result)
	default:
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
				"token_type":          authorizationTypeBearer,
				"access_token":        "access-token",
				authParamRefreshToken: "refresh-token",
				"expires_in":          60,
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
	if !IsInvalidGrant(err) {
		t.Fatalf("invalid grant was not classified: %v", err)
	}
}

func TestIsInvalidGrantRejectsOtherAuthenticationErrors(t *testing.T) {
	t.Parallel()
	if IsInvalidGrant(&APIError{Status: http.StatusUnauthorized, Code: "invalid_client"}) ||
		IsInvalidGrant(errors.New("invalid_grant")) {
		t.Fatal("non-typed invalid grant error was classified as an expired login")
	}
}

func writeTokenResponse(t *testing.T, writer http.ResponseWriter) {
	t.Helper()
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"token_type": authorizationTypeBearer, "access_token": "access-token", authParamRefreshToken: "refresh-token",
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
		TokenType:        authorizationTypeBearer,
		AccessToken:      "old-access",
		RefreshToken:     "old-refresh",
		DeviceID:         "device-1",
		AccessExpiresAt:  time.Now().Add(time.Minute),
		RefreshExpiresAt: time.Now().Add(time.Hour),
		IdentityID:       "identity-1",
		UserName:         "Example User",
	}
}

func testIDToken() string {
	payload := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"sub":"identity-1","name":"Example User","email":"user@example.test"}`),
	)
	return "header." + payload + ".signature"
}
