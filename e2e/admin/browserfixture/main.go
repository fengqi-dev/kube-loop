// Command browserfixture starts the real Control Plane Management Plane UI and
// persistence stack on loopback for interactive browser acceptance testing.
package main

import (
	"bytes"
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
	adminrevision "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/revision"
	adminsession "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/session"
	admintoken "github.com/fengqi-dev/kube-loop/internal/controlplane/authn/token"
	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

const (
	fixtureCredential  = "valid"
	fixtureAccessToken = "browser-fixture-access-token"
	fixturePrincipalID = "11111111-1111-4111-8111-111111111111"
	fixtureFamilyID    = "22222222-2222-4222-8222-222222222222"
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

func (value tokenAuthenticator) Authenticate(_ context.Context, token string) (admintoken.AccessIdentity, error) {
	if token != fixtureAccessToken {
		return admintoken.AccessIdentity{}, errors.New("invalid fixture access token")
	}
	return admintoken.AccessIdentity{
		Principal: value.principal, FamilyID: fixtureFamilyID, DeviceID: "browser-fixture-device",
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
	principal, err := store.Principals().Upsert(ctx, controlplanestorage.Principal{
		ID: fixturePrincipalID, Provider: "browser-fixture", ExternalID: "administrator",
		DisplayName: "Browser Administrator", Groups: []string{"browser-admins"}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := store.TokenFamilies().Create(ctx, controlplanestorage.TokenFamily{
		ID: fixtureFamilyID, PrincipalID: fixturePrincipalID, DeviceID: "browser-fixture-device",
		RefreshTokenHash: bytes.Repeat([]byte{7}, 32), CreatedAt: now, ExpiresAt: now.Add(time.Hour),
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
		Version: adminauthorization.CurrentVersion, Revision: 1,
		Assignments: []adminauthorization.Assignment{{
			ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Role: adminauthorization.RolePlatformAdmin,
			Subjects: []string{fixturePrincipalID},
		}},
	}, adminauthorization.WithBreakGlass(breakGlass))
	if err != nil {
		log.Fatal(err)
	}
	loader, err := adminrevision.NewPolicyLoader(store, authorizer, 0)
	if err != nil {
		log.Fatal(err)
	}
	revisions, err := adminrevision.New(store)
	if err != nil {
		log.Fatal(err)
	}
	management, err := adminhttpapi.New(adminhttpapi.Config{PublicURL: "http://" + *listenAddress}, sessions,
		adminhttpapi.WithReadAPI(authorizer, store, adminhttpapi.BuildInfo{
			Version: "e2e", Commit: "browser-fixture", ProtocolMin: "2.0", ProtocolMax: "2.0",
		}),
		adminhttpapi.WithPolicyAPI(revisions, loader),
		adminhttpapi.WithTokenExchange(tokenAuthenticator{principal: principal}),
	)
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/v2/admin/", http.StripPrefix("/api/v2/admin", management))
	mux.HandleFunc("/auth/ad/e2e-ad/login", func(writer http.ResponseWriter, request *http.Request) {
		var input struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if request.Method != http.MethodPost || json.NewDecoder(request.Body).Decode(&input) != nil ||
			input.Username != "administrator" || input.Password != "password" {
			http.Error(writer, `{"error":{"message":"fixture authentication failed"}}`, http.StatusUnauthorized)
			return
		}
		writeTokens(writer)
	})
	mux.HandleFunc("/auth/oidc/e2e-oidc/start", func(writer http.ResponseWriter, request *http.Request) {
		var input struct {
			ClientCallback string `json:"clientCallback"`
			State          string `json:"state"`
		}
		if request.Method != http.MethodPost || json.NewDecoder(request.Body).Decode(&input) != nil ||
			input.ClientCallback != "http://"+*listenAddress+"/api/v2/admin/ui/callback" || input.State == "" {
			http.Error(writer, `{"error":{"message":"fixture OIDC request failed"}}`, http.StatusBadRequest)
			return
		}
		authorize := url.URL{Scheme: "http", Host: *listenAddress, Path: "/mock/oidc/authorize"}
		query := authorize.Query()
		query.Set("callback", input.ClientCallback)
		query.Set("state", input.State)
		authorize.RawQuery = query.Encode()
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]string{"authorizationUrl": authorize.String()})
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
	mux.HandleFunc("/auth/token/exchange", func(writer http.ResponseWriter, request *http.Request) {
		var input struct {
			Code string `json:"code"`
		}
		if request.Method != http.MethodPost || json.NewDecoder(request.Body).Decode(&input) != nil ||
			input.Code != "fixture-authorization-code" {
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
				map[string]string{"id": "e2e-ad", "type": "ad", "displayName": "Fixture AD", "interaction": "password"},
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
	log.Printf("management browser fixture: http://%s/api/v2/admin/ui/ (credential %q)", *listenAddress, fixtureCredential)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func writeTokens(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"tokenType": "Bearer", "accessToken": fixtureAccessToken,
		"accessExpiresAt": time.Now().Add(5 * time.Minute), "refreshToken": "browser-fixture-refresh-token",
		"refreshExpiresAt": time.Now().Add(time.Hour),
	})
}
