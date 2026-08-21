package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientdiscovery "github.com/fengqi-dev/kube-loop/internal/client/discovery"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

//nolint:gocyclo // The integration flow intentionally validates one complete clean-directory startup lifecycle.
func TestCleanDirectoryWithOnlyServerURLBrowsesRemoteInventory(t *testing.T) {
	cleanDirectory := t.TempDir()
	profilePath := filepath.Join(cleanDirectory, "servers.json")
	if entries, err := os.ReadDir(cleanDirectory); err != nil || len(entries) != 0 {
		t.Fatalf("initial directory = %v, %v", entries, err)
	}
	network, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.244.0.0/16"}, ServiceIPs: []string{"10.96.0.10"},
		DNSServer: "10.96.0.10", ClusterDomains: []string{"cluster.local"},
	})
	if err != nil {
		t.Fatal(err)
	}
	networkHash, err := networkspec.Hash(network)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.NewString()
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != clientdiscovery.Path && request.URL.Path != "/.well-known/openid-configuration" &&
			request.URL.Path != "/oauth2/token" &&
			request.Header.Get("Authorization") != "Bearer clean-access" {
			t.Errorf("%s Authorization = %q", request.URL.Path, request.Header.Get("Authorization"))
		}
		switch request.URL.Path {
		case clientdiscovery.Path:
			_ = json.NewEncoder(writer).Encode(clientdiscovery.Document{
				ServiceID:     "clean-service",
				PublicURL:     server.URL,
				TunnelPath:    "/tunnel",
				APIVersions:   []string{"v2"},
				ProtocolMin:   "2.0",
				ProtocolMax:   "2.0",
				ServerVersion: "2.0.0",
				AuthMethods: []clientdiscovery.AuthMethod{
					{ID: "local", Type: "local", Interaction: authenticationProviderBrowser},
				},
			})
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"issuer":                 server.URL,
				"authorization_endpoint": server.URL + "/oauth2/authorize",
				"token_endpoint":         server.URL + "/oauth2/token",
				"revocation_endpoint":    server.URL + "/oauth2/revoke",
			})
		case "/oauth2/token":
			writeAppTokenResponse(writer, "clean-access", "clean-refresh")
		case "/api/version":
			_ = json.NewEncoder(writer).Encode(clientremote.Version{GitVersion: "v1.31.2", GatewayVersion: "v2-clean"})
		case "/api/namespaces":
			_, _ = writer.Write([]byte(`{"items":[{"name":"development","status":"Active"}]}`))
		case "/api/sessions":
			now := time.Now().UTC()
			snapshot := clientremote.Capabilities{
				SchemaVersion: 1, IdentityID: "identity-clean", Namespace: "development",
				GatewayVersion: "v2-clean", Capabilities: []string{"pods.list"},
			}
			_ = json.NewEncoder(writer).Encode(clientremote.Session{
				ID: sessionID, Namespace: "development", State: "active", Generation: 1,
				CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(2 * time.Minute),
				NetworkSpec: network, NetworkSpecHash: networkHash, Capabilities: &snapshot,
			})
		case "/api/namespaces/development/pods":
			_, _ = writer.Write(
				[]byte(
					`{"items":[{"name":"api-0","namespace":"development","phase":"Running","ready":true,"containers":["api"]}]}`,
				),
			)
		case "/api/sessions/" + sessionID:
			now := time.Now().UTC()
			_ = json.NewEncoder(writer).Encode(clientremote.Session{
				ID:         sessionID,
				Namespace:  "development",
				State:      remoteStateDisconnected,
				Generation: 2,
				CreatedAt: now.Add(
					-time.Minute,
				),
				UpdatedAt:       now,
				LastHeartbeatAt: now.Add(-time.Minute),
				ExpiresAt:       now.Add(time.Minute),
				NetworkSpec:     network,
				NetworkSpecHash: networkHash,
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	credentialStore := &memoryCredentialStore{values: map[string]credentials.Credential{}}
	application := newApp("2.0.0", nil, appDependencies{
		profilePath: profilePath, credentialStore: credentialStore, httpClient: server.Client(),
	})
	t.Cleanup(func() { application.shutdown(context.Background()) })
	bootstrap, err := application.Bootstrap()
	if err != nil || len(bootstrap.ServerProfiles.Profiles) != 0 {
		t.Fatalf("clean bootstrap = %#v, %v", bootstrap, err)
	}
	profileResult, err := application.SaveServerProfile(SaveServerProfileRequest{BaseURL: server.URL, Activate: true})
	if err != nil || profileResult.Profile.ID != "clean-service" {
		t.Fatalf("save URL-only profile = %#v, %v", profileResult, err)
	}
	if err := credentialStore.Set("clean-service", credentials.Credential{
		AccessToken: "clean-access", RefreshToken: "clean-refresh", DeviceID: "clean-device",
		AccessExpiresAt: time.Now().Add(time.Hour), RefreshExpiresAt: time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	inventory, err := application.LoadServerInventory("clean-service", "")
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Namespace != "development" || len(inventory.Pods) != 1 || inventory.Pods[0].Name != "api-0" ||
		inventory.Session == nil || inventory.DataPlane != nil || len(inventory.Services) != 0 {
		t.Fatalf("remote-only inventory = %#v", inventory)
	}
	if _, err := os.Stat(profilePath); err != nil {
		t.Fatalf("Server Profile was not persisted in clean directory: %v", err)
	}
	for _, directory := range []string{"config", "data", "state", "secrets", "cache"} {
		info, err := os.Stat(filepath.Join(cleanDirectory, directory))
		if err != nil || !info.IsDir() {
			t.Fatalf("standard user directory %q is unavailable: %v", directory, err)
		}
	}
	if _, err := os.Stat(filepath.Join(cleanDirectory, "config", "kubeconfig")); !os.IsNotExist(err) {
		t.Fatalf("unexpected kubeconfig file exists: %v", err)
	}
}
