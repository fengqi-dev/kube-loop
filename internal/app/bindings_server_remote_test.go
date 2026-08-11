package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	clientauth "github.com/fengqi-dev/kube-loop/internal/client/auth"
	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	clientremotesession "github.com/fengqi-dev/kube-loop/internal/client/remotesession"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/google/uuid"
)

func TestLoadServerInventoryUsesCapabilitiesAndRemembersNamespace(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.2.0.0/16"}, ServiceCIDRs: []string{"10.96.0.0/12"},
		ServiceIPs: []string{"10.96.0.10"}, DNSServer: "10.96.0.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	specHash, err := networkspec.Hash(spec)
	if err != nil {
		t.Fatal(err)
	}
	var serviceCalls int
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access-token" {
			t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
		}
		switch request.URL.Path {
		case "/api/v2/version":
			_, _ = writer.Write([]byte(`{"gitVersion":"v1.31.2","gatewayVersion":"v2-test"}`))
		case "/api/v2/namespaces":
			_, _ = writer.Write([]byte(`{"items":[{"name":"production","status":"Active"},{"name":"development","status":"Active"}]}`))
		case "/api/v2/capabilities":
			if request.URL.Query().Get("namespace") != "development" {
				t.Fatalf("namespace query = %q", request.URL.Query().Get("namespace"))
			}
			_, _ = writer.Write([]byte(`{"schemaVersion":1,"principalId":"principal-1","namespace":"development","gatewayVersion":"v2-test","capabilities":["pods.list"]}`))
		case "/api/v2/sessions":
			now := time.Now().UTC()
			_ = json.NewEncoder(writer).Encode(clientremote.Session{
				ID: uuid.NewString(), Namespace: "development", State: "active", Generation: 1,
				CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(2 * time.Minute),
				NetworkSpec: spec, NetworkSpecHash: specHash,
				Capabilities: &clientremote.Capabilities{
					SchemaVersion: 1, PrincipalID: "principal-1", Namespace: "development",
					GatewayVersion: "v2-test", Capabilities: []string{"pods.list"},
				},
			})
		case "/api/v2/namespaces/development/pods":
			_, _ = writer.Write([]byte(`{"items":[{"name":"api-1","namespace":"development","phase":"Running","ready":true,"containers":["api"]},{"name":"api-0","namespace":"development","phase":"Pending","ready":false,"containers":["api"]}]}`))
		case "/api/v2/namespaces/development/services":
			serviceCalls++
			_, _ = writer.Write([]byte(`{"items":[]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := profileStore.Upsert(clientprofile.Profile{ID: "service-1", BaseURL: server.URL}); err != nil {
		t.Fatal(err)
	}
	credentialStore := &memoryCredentialStore{values: map[string]credentials.Credential{"service-1": {
		AccessToken: "access-token", RefreshToken: "refresh-token", DeviceID: "device-1",
		AccessExpiresAt: time.Now().Add(time.Minute), RefreshExpiresAt: time.Now().Add(time.Hour),
	}}}
	authClient := clientauth.New(clientauth.Config{HTTPClient: server.Client()})
	remoteClient, err := clientremote.New(credentialStore, authClient, clientremote.Config{HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	remoteSessions, err := clientremotesession.New(remoteClient, clientremotesession.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = remoteSessions.Shutdown(ctx)
	}()
	application := &App{
		profiles: profileStore, credentials: credentialStore, auth: authClient,
		remote: remoteClient, remoteSessions: remoteSessions,
	}
	result, err := application.LoadServerInventory("service-1", "development")
	if err != nil {
		t.Fatal(err)
	}
	if result.KubernetesVersion != "v1.31.2" || result.GatewayVersion != "v2-test" || result.Namespace != "development" {
		t.Fatalf("inventory = %#v", result)
	}
	if result.Session == nil || result.Session.State != "active" || result.Network == nil {
		t.Fatalf("remote Session = %#v", result.Session)
	}
	if len(result.Namespaces) != 2 || result.Namespaces[0].Name != "development" || len(result.Pods) != 2 || result.Pods[0].Name != "api-0" {
		t.Fatalf("inventory sorting = %#v", result)
	}
	if len(result.Services) != 0 || serviceCalls != 0 {
		t.Fatalf("unauthorized services were requested: result=%#v calls=%d", result.Services, serviceCalls)
	}
	if profileStore.Snapshot().Profiles[0].LastNamespace != "development" {
		t.Fatalf("profile = %#v", profileStore.Snapshot().Profiles[0])
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "access-token") || strings.Contains(string(raw), "refresh-token") {
		t.Fatalf("credentials leaked into Wails result: %s", raw)
	}
}
