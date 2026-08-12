package auth_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	clientauth "github.com/fengqi-dev/kube-loop/internal/client/auth"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/httpauth"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/login"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/oidc"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/token"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/labstack/echo/v5"
)

type oidcAuthorization struct {
	nonce     string
	challenge string
}

func TestFullStackOIDCLoginRefreshAndRevoke(t *testing.T) {
	providerServer := httptest.NewUnstartedServer(nil)
	providerURL := "https://" + providerServer.Listener.Addr().String()
	controllerServer := httptest.NewUnstartedServer(nil)
	controllerURL := "https://" + controllerServer.Listener.Addr().String()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: privateKey}, &jose.SignerOptions{
		ExtraHeaders: map[jose.HeaderKey]any{jose.HeaderKey("kid"): "oidc-e2e"},
	})
	if err != nil {
		t.Fatal(err)
	}
	publicJWK := jose.JSONWebKey{Key: &privateKey.PublicKey, KeyID: "oidc-e2e", Algorithm: "RS256", Use: "sig"}
	var authorizationMu sync.Mutex
	authorizations := make(map[string]oidcAuthorization)
	providerServer.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			writeOIDCJSON(writer, map[string]any{
				"issuer": providerURL, "authorization_endpoint": providerURL + "/authorize",
				"token_endpoint": providerURL + "/token", "jwks_uri": providerURL + "/jwks",
				"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
				"id_token_signing_alg_values_supported": []string{"RS256"},
				"code_challenge_methods_supported":      []string{"S256"},
				"claims_supported":                      []string{"sub", "name", "email", "groups"},
			})
		case "/jwks":
			writeOIDCJSON(writer, map[string]any{"keys": []jose.JSONWebKey{publicJWK}})
		case "/authorize":
			query := request.URL.Query()
			if query.Get("client_id") != "desktop-client" || query.Get("response_type") != "code" ||
				query.Get("redirect_uri") != controllerURL+"/auth/callback/corporate" ||
				query.Get("code_challenge_method") != "S256" || len(query.Get("nonce")) < 32 ||
				len(query.Get("state")) < 32 || len(query.Get("code_challenge")) < 43 {
				http.Error(writer, "invalid authorization request", http.StatusBadRequest)
				return
			}
			code := "upstream-authorization-code"
			authorizationMu.Lock()
			authorizations[code] = oidcAuthorization{nonce: query.Get("nonce"), challenge: query.Get("code_challenge")}
			authorizationMu.Unlock()
			callback, _ := url.Parse(query.Get("redirect_uri"))
			values := callback.Query()
			values.Set("code", code)
			values.Set("state", query.Get("state"))
			callback.RawQuery = values.Encode()
			http.Redirect(writer, request, callback.String(), http.StatusSeeOther)
		case "/token":
			if err := request.ParseForm(); err != nil {
				http.Error(writer, "invalid token request", http.StatusBadRequest)
				return
			}
			code := request.Form.Get("code")
			authorizationMu.Lock()
			authorization, ok := authorizations[code]
			delete(authorizations, code)
			authorizationMu.Unlock()
			verifierHash := sha256.Sum256([]byte(request.Form.Get("code_verifier")))
			if !ok || base64.RawURLEncoding.EncodeToString(verifierHash[:]) != authorization.challenge {
				http.Error(writer, "invalid authorization code", http.StatusBadRequest)
				return
			}
			now := time.Now().UTC()
			idToken, signErr := jwt.Signed(signer).Claims(jwt.Claims{
				Issuer: providerURL, Subject: "directory-object-123", Audience: jwt.Audience{"desktop-client"},
				Expiry: jwt.NewNumericDate(now.Add(time.Minute)), IssuedAt: jwt.NewNumericDate(now),
			}).Claims(map[string]any{
				"nonce": authorization.nonce, "name": "Ada Lovelace", "email": "ada@example.test",
				"groups": []string{"developers", "platform"},
			}).Serialize()
			if signErr != nil {
				http.Error(writer, "sign token", http.StatusInternalServerError)
				return
			}
			writeOIDCJSON(writer, map[string]any{
				"access_token": "upstream-access-token", "token_type": "Bearer", "expires_in": 60, "id_token": idToken,
			})
		default:
			http.NotFound(writer, request)
		}
	})
	providerServer.StartTLS()
	defer providerServer.Close()

	provider, err := oidc.New(context.Background(), oidc.Config{
		ID: "corporate", DisplayName: "Corporate OIDC", Issuer: providerURL,
		ClientID: "desktop-client", ClientSecret: "client-secret",
		RedirectURL: controllerURL + "/auth/callback/corporate", HTTPClient: providerServer.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := authn.NewRegistry(provider)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "oidc-fullstack.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loginService, err := login.New(registry, store, login.Config{})
	if err != nil {
		t.Fatal(err)
	}
	_, tokenKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tokenService, err := token.New(store, token.Config{
		Issuer: controllerURL, KeyID: "control-plane-e2e", SigningKey: tokenKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	authService, err := httpauth.New(loginService, tokenService)
	if err != nil {
		t.Fatal(err)
	}
	authRouter := echo.New()
	httpauth.NewRoutes(authService).RegisterRoutes(authRouter.Group("/auth"))
	controllerServer.Config.Handler = authRouter
	controllerServer.StartTLS()
	defer controllerServer.Close()

	roots := x509.NewCertPool()
	roots.AddCert(providerServer.Certificate())
	roots.AddCert(controllerServer.Certificate())
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	browserClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	client := clientauth.New(clientauth.Config{
		HTTPClient: httpClient, RequestTimeout: 5 * time.Second, LoginTimeout: 10 * time.Second,
		OpenBrowser: func(target string) error {
			response, browserErr := browserClient.Get(target)
			if browserErr != nil {
				return browserErr
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				return fmt.Errorf("browser flow returned HTTP %d", response.StatusCode)
			}
			return nil
		},
	})
	credential, err := client.LoginOIDC(context.Background(), controllerURL, "corporate", "desktop-e2e")
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccessToken == "" || credential.RefreshToken == "" || credential.DeviceID != "desktop-e2e" {
		t.Fatalf("OIDC credential = %#v", credential)
	}
	principals, err := store.Principals().List(context.Background(), storage.PrincipalListFilter{Limit: 10})
	if err != nil || len(principals) != 1 || principals[0].DisplayName != "Ada Lovelace" ||
		!strings.Contains(strings.Join(principals[0].Groups, ","), "platform") {
		t.Fatalf("persisted principals = %#v err=%v", principals, err)
	}
	refreshed, err := client.Refresh(context.Background(), controllerURL, credential)
	if err != nil || refreshed.AccessToken == credential.AccessToken || refreshed.RefreshToken == credential.RefreshToken {
		t.Fatalf("refreshed credential = %#v err=%v", refreshed, err)
	}
	if err := client.Revoke(context.Background(), controllerURL, refreshed.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Refresh(context.Background(), controllerURL, refreshed); err == nil {
		t.Fatal("revoked OIDC token family was refreshed")
	}
}

func writeOIDCJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}
