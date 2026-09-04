package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

func TestCapabilitiesValidateNamespaceAndResponseBinding(t *testing.T) {
	now := time.Now()
	store := &memoryStore{value: validCredential(now)}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(Capabilities{
			SchemaVersion:  1,
			IdentityID:     "identity-1",
			Namespace:      "other",
			GatewayVersion: "v2-test",
			Capabilities:   []string{},
		})
	}))
	defer server.Close()
	client, err := New(
		store,
		&fakeRefresher{now: now},
		Config{HTTPClient: server.Client(), Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service-1", BaseURL: server.URL}
	if _, err := client.Capabilities(context.Background(), serverProfile, "Bad_Name"); err == nil {
		t.Fatal("invalid namespace was accepted")
	}
	if _, err := client.Capabilities(context.Background(), serverProfile, "development"); err == nil {
		t.Fatal("capabilities for a different namespace were accepted")
	}
}

func TestCapabilitiesCacheIsBoundedByIdentityNamespaceCredentialAndGatewayVersion(t *testing.T) {
	now := time.Now().UTC()
	store := &memoryStore{value: validCredential(now)}
	var capabilityCalls atomic.Int32
	gatewayVersion := "v2-a"
	identityID := "identity-a"
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/version":
			_ = json.NewEncoder(writer).Encode(Version{GitVersion: "v1.31.0", GatewayVersion: gatewayVersion})
		case "/api/capabilities":
			capabilityCalls.Add(1)
			_ = json.NewEncoder(writer).Encode(Capabilities{
				SchemaVersion: 1, IdentityID: identityID, Namespace: request.URL.Query().Get(remoteParamNamespace),
				GatewayVersion: gatewayVersion, Capabilities: []string{"pods.list"},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := New(store, &fakeRefresher{now: now}, Config{
		HTTPClient: server.Client(), Now: func() time.Time { return now }, CapabilityCacheTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service-1", BaseURL: server.URL}
	if _, err := client.Version(context.Background(), serverProfile); err != nil {
		t.Fatal(err)
	}
	first, err := client.Capabilities(context.Background(), serverProfile, "development")
	if err != nil {
		t.Fatal(err)
	}
	first.Capabilities[0] = "mutated"
	second, err := client.Capabilities(context.Background(), serverProfile, "development")
	if err != nil || capabilityCalls.Load() != 1 || second.Capabilities[0] != "pods.list" {
		t.Fatalf("cached result = %#v, calls = %d, error = %v", second, capabilityCalls.Load(), err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := client.Capabilities(
		context.Background(),
		serverProfile,
		"development",
	); err != nil ||
		capabilityCalls.Load() != 2 {
		t.Fatalf("expired cache calls = %d, error = %v", capabilityCalls.Load(), err)
	}
	gatewayVersion = "v2-b"
	if _, err := client.Version(context.Background(), serverProfile); err != nil {
		t.Fatal(err)
	}
	updated, err := client.Capabilities(context.Background(), serverProfile, "development")
	if err != nil || capabilityCalls.Load() != 3 || updated.GatewayVersion != "v2-b" {
		t.Fatalf("Gateway-version cache binding = %#v, calls = %d, error = %v", updated, capabilityCalls.Load(), err)
	}
	store.mu.Lock()
	store.value.RefreshToken = "refresh-other-identity"
	store.value.AccessToken = "access-other-identity"
	store.mu.Unlock()
	identityID = "identity-b"
	updated, err = client.Capabilities(context.Background(), serverProfile, "development")
	if err != nil || capabilityCalls.Load() != 4 || updated.IdentityID != "identity-b" {
		t.Fatalf("identity cache binding = %#v, calls = %d, error = %v", updated, capabilityCalls.Load(), err)
	}
	if _, err := client.Capabilities(
		context.Background(),
		serverProfile,
		"staging",
	); err != nil ||
		capabilityCalls.Load() != 5 {
		t.Fatalf("namespace cache binding calls = %d, error = %v", capabilityCalls.Load(), err)
	}
}
