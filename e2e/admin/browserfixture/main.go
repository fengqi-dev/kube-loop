// Command browserfixture starts the real Control Plane Management Plane UI and
// persistence stack on loopback for interactive browser acceptance testing.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminhttpapi "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/httpapi"
	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	adminoperations "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/operations"
	adminconfig "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/managementconfig"
	adminsession "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/session"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/labstack/echo/v5"
)

const (
	fixtureCredential      = "valid"
	fixtureAccessToken     = "browser-fixture-access-token"
	fixtureAuthorizationID = "22222222-2222-4222-8222-222222222222"
)

type verifier struct{ generation string }

func (value *verifier) Verify(_ context.Context, _ netip.Addr, credential []byte) (string, error) {
	defer clear(credential)
	if string(credential) != fixtureCredential {
		return "", errors.New("invalid fixture credential")
	}
	return value.generation, nil
}

func (*verifier) SessionTTL() time.Duration { return 15 * time.Minute }

func (value *verifier) CurrentBreakGlassState(context.Context) (adminauthorization.BreakGlassState, error) {
	return adminauthorization.BreakGlassState{Enabled: true, Generation: value.generation}, nil
}

type tokenAuthenticator struct {
	principal controlplanestorage.Principal
}

type fixtureSessionRuntime struct{}

func (fixtureSessionRuntime) Disconnect(context.Context, string) error { return nil }

type fixtureProviderLifecycle struct{}

func (fixtureProviderLifecycle) Validate(context.Context, adminconfig.ProviderCandidate) (json.RawMessage, error) {
	return json.RawMessage(`{"valid":true,"fixture":true}`), nil
}

func (fixtureProviderLifecycle) Prepare(context.Context, adminconfig.ProviderCandidate) (func(), error) {
	return func() {}, nil
}

func (value tokenAuthenticator) Authenticate(_ context.Context, token string) (authn.AccessIdentity, error) {
	if token != fixtureAccessToken {
		return authn.AccessIdentity{}, errors.New("invalid fixture access token")
	}
	return authn.AccessIdentity{
		Principal: value.principal, AuthorizationID: fixtureAuthorizationID, DeviceID: "browser-fixture-device",
		TokenID: "33333333-3333-4333-8333-333333333333", AccessExpiresAt: time.Now().Add(5 * time.Minute),
	}, nil
}

func main() {
	listenAddress := flag.String("listen", "127.0.0.1:18181", "loopback listen address")
	databasePath := flag.String("database", "", "SQLite path; defaults to a temporary file")
	flag.Parse()

	host, _, err := net.SplitHostPort(*listenAddress)
	if err != nil {
		log.Fatal(err)
	}
	address, err := netip.ParseAddr(host)
	if err != nil || !address.IsLoopback() {
		log.Fatal("browser fixture must listen on a loopback address")
	}

	path := strings.TrimSpace(*databasePath)
	if path == "" {
		directory, err := os.MkdirTemp("", "kubeloop-admin-browser-")
		if err != nil {
			log.Fatal(err)
		}
		defer os.RemoveAll(directory)
		path = filepath.Join(directory, "management.db")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, err := controlplanestorage.Open(ctx, controlplanestorage.Config{
		Backend: controlplanestorage.BackendSQLite, SQLitePath: path,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	localUsers, err := adminlocaluser.New(store, []byte("0123456789abcdef0123456789abcdef"), "KubeLoop Browser Fixture")
	if err != nil {
		log.Fatal(err)
	}
	localUser, _, err := localUsers.EnsureInitial(ctx, adminlocaluser.CreateRequest{
		Username: "browser-admin", Password: []byte("Browser-Fixture-Password-2026!"),
		DisplayName: "Browser Administrator", Email: "browser-admin@example.test",
	})
	if err != nil {
		log.Fatal(err)
	}
	principal, err := store.Principals().GetByID(ctx, localUser.PrincipalID)
	if err != nil {
		log.Fatal(err)
	}
	signatureHash := sha256.Sum256([]byte("browser-fixture-access-token"))
	if err := store.OAuthSessions().Create(ctx, controlplanestorage.OAuthSession{
		Kind: "access_token", SignatureHash: signatureHash[:], RequestID: fixtureAuthorizationID,
		PrincipalID: principal.ID, ClientID: "kubeloop-management", DeviceID: "browser-fixture-device",
		RequestJSON: json.RawMessage(`{}`), CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		log.Fatal(err)
	}

	digest := sha256.Sum256([]byte("kubeloop-browser-fixture"))
	breakGlass := &verifier{generation: base64.RawURLEncoding.EncodeToString(digest[:])}
	sessions, err := adminsession.New(store, breakGlass)
	if err != nil {
		log.Fatal(err)
	}
	authorizer, err := adminauthorization.New(adminauthorization.Snapshot{
		Version: adminauthorization.CurrentVersion,
		Bindings: []adminauthorization.Binding{{
			ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", RoleID: adminauthorization.RolePlatformAdmin,
			Subject:   adminauthorization.SubjectRef{Type: adminauthorization.SubjectPrincipal, PrincipalID: principal.ID},
			Scope:     adminauthorization.BindingScope{Type: adminauthorization.ScopePlatform},
			ManagedBy: adminauthorization.ManagedByPlatform,
		}},
	}, adminauthorization.WithBreakGlass(breakGlass))
	if err != nil {
		log.Fatal(err)
	}
	loader, err := adminconfig.NewPolicyLoader(store, authorizer, 0)
	if err != nil {
		log.Fatal(err)
	}
	revisions, err := adminconfig.New(store)
	if err != nil {
		log.Fatal(err)
	}
	providerLifecycle := fixtureProviderLifecycle{}
	providers, err := adminconfig.NewProviderService(store, providerLifecycle, providerLifecycle)
	if err != nil {
		log.Fatal(err)
	}
	operations, err := adminoperations.New(store, fixtureSessionRuntime{})
	if err != nil {
		log.Fatal(err)
	}
	if err := operations.ConfigureRecovery(adminoperations.RecoveryRunnerFunc(func(context.Context) (map[string]int, error) {
		return map[string]int{"browser-fixture": 0}, nil
	})); err != nil {
		log.Fatal(err)
	}
	go operations.Run(ctx)
	management, err := adminhttpapi.New(adminhttpapi.Config{PublicURL: "http://" + *listenAddress}, sessions,
		adminhttpapi.WithReadAPI(authorizer, store, adminhttpapi.BuildInfo{
			Version: "e2e", Commit: "browser-fixture", ProtocolMin: "2.0", ProtocolMax: "2.0",
		}),
		adminhttpapi.WithPolicyAPI(revisions, loader),
		adminhttpapi.WithProviderAPI(providers),
		adminhttpapi.WithOAuthClients(store, store),
		adminhttpapi.WithOperationsAPI(operations),
		adminhttpapi.WithLocalUsers(localUsers),
		adminhttpapi.WithTokenExchange(tokenAuthenticator{principal: principal}),
	)
	if err != nil {
		log.Fatal(err)
	}

	router := echo.New()
	management.RegisterRoutes(router.Group("/api/admin"))
	mux := http.NewServeMux()
	mux.Handle("/api/admin/", router)
	mux.HandleFunc("/oauth2/authorize", func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if request.Method != http.MethodGet || query.Get("response_type") != "code" ||
			query.Get("client_id") != "kubeloop-management" || query.Get("provider") != "e2e-oidc" ||
			query.Get("redirect_uri") != "http://"+*listenAddress+"/api/admin/ui/callback" ||
			query.Get("state") == "" || query.Get("code_challenge_method") != "S256" {
			http.Error(writer, `{"error":{"message":"fixture OIDC request failed"}}`, http.StatusBadRequest)
			return
		}
		authorize := url.URL{Scheme: "http", Host: *listenAddress, Path: "/mock/oidc/authorize"}
		upstreamQuery := authorize.Query()
		upstreamQuery.Set("callback", query.Get("redirect_uri"))
		upstreamQuery.Set("state", query.Get("state"))
		authorize.RawQuery = upstreamQuery.Encode()
		http.Redirect(writer, request, authorize.String(), http.StatusFound)
	})
	mux.HandleFunc("/mock/oidc/authorize", func(writer http.ResponseWriter, request *http.Request) {
		callback, err := url.Parse(request.URL.Query().Get("callback"))
		if err != nil || callback.Scheme != "http" || callback.Host != *listenAddress {
			http.Error(writer, "invalid fixture callback", http.StatusBadRequest)
			return
		}
		query := callback.Query()
		query.Set("code", "fixture-authorization-code")
		query.Set("state", request.URL.Query().Get("state"))
		callback.RawQuery = query.Encode()
		http.Redirect(writer, request, callback.String(), http.StatusFound)
	})
	mux.HandleFunc("/oauth2/token", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.ParseForm() != nil ||
			request.Form.Get("grant_type") != "authorization_code" ||
			request.Form.Get("client_id") != "kubeloop-management" ||
			request.Form.Get("code") != "fixture-authorization-code" {
			http.Error(writer, `{"error":{"message":"fixture token exchange failed"}}`, http.StatusUnauthorized)
			return
		}
		writeTokens(writer)
	})
	mux.HandleFunc("/.well-known/kubeloop", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"serviceId": "browser-fixture", "authMethods": []any{
				map[string]string{"id": "e2e-oidc", "type": "oidc", "displayName": "Fixture OIDC", "interaction": "browser"},
			},
		})
	})

	server := &http.Server{Addr: *listenAddress, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()
	log.Printf("management browser fixture: http://%s/api/admin/ui/", *listenAddress)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func writeTokens(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"token_type": "Bearer", "access_token": fixtureAccessToken, "expires_in": 300,
		"refresh_token": "browser-fixture-refresh-token",
	})
}
