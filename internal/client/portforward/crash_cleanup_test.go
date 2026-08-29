package portforward

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/maintenance"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/portforwardapi"
	portforwardservice "github.com/fengqi-dev/kube-loop/internal/controlplane/portforwardapi/service"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/capability"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

type crashCredentialStore struct {
	mu         sync.Mutex
	profileID  string
	credential credentials.Credential
}

func (store *crashCredentialStore) Set(profileID string, credential credentials.Credential) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if profileID != store.profileID {
		return credentials.ErrNotFound
	}
	store.credential = credential
	return nil
}

func (store *crashCredentialStore) Get(profileID string) (credentials.Credential, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if profileID != store.profileID {
		return credentials.Credential{}, credentials.ErrNotFound
	}
	return store.credential, nil
}

func (store *crashCredentialStore) Delete(profileID string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if profileID != store.profileID {
		return credentials.ErrNotFound
	}
	store.credential = credentials.Credential{}
	return nil
}

type crashTokenRefresher struct{}

func (crashTokenRefresher) Refresh(
	context.Context,
	string,
	credentials.Credential,
) (credentials.Credential, error) {
	return credentials.Credential{}, errors.New("unexpected token refresh")
}

type crashNetworkDiscoverer struct {
	spec networkspec.Spec
}

type crashCapabilityDiscoverer struct{}

func (crashCapabilityDiscoverer) DiscoverCapabilities(
	context.Context,
	controlplaneapi.Identity,
	string,
) (capability.Snapshot, *controlplaneapi.Error) {
	return capability.Snapshot{}, nil
}

func (discoverer crashNetworkDiscoverer) Discover(
	context.Context,
	controlplaneapi.Identity,
	string,
) (networkspec.Spec, error) {
	return discoverer.spec, nil
}

type crashTargetResolver struct{}

type crashBindingManager struct{}

func (crashBindingManager) Activate(
	context.Context,
	sessionapi.ActiveSession,
	string,
	portforwardservice.Spec,
) (bool, error) {
	return true, nil
}

func (crashBindingManager) Delete(context.Context, string, string) error { return nil }
func (crashBindingManager) Stop(context.Context, string, string) error   { return nil }

func (crashTargetResolver) Resolve(
	context.Context,
	controlplaneapi.Identity,
	string,
	portforwardservice.Spec,
) (portforwardservice.Target, error) {
	return portforwardservice.Target{Host: "10.96.0.20", Port: 8080}, nil
}

func TestCrashedClientTaskIsReclaimedAfterSessionExpiry(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 13, 0, 0, 0, time.UTC)
	expiresAt := now.Add(time.Minute)
	stateStore, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "crash-cleanup.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	identityID := uuid.NewString()
	sessionID := uuid.NewString()
	deviceID := "crashed-desktop"
	if _, err := stateStore.Identities().Create(ctx, storage.Identity{
		ID:          identityID,
		Type:        "human",
		DisplayName: "Test Identity",
		Status:      portForwardSessionActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}
	network, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{"10.96.0.20"}})
	if err != nil {
		t.Fatal(err)
	}
	networkJSON, err := networkspec.CanonicalJSON(network)
	if err != nil {
		t.Fatal(err)
	}
	networkHash, err := networkspec.Hash(network)
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Sessions().Create(ctx, storage.Session{
		ID: sessionID, IdentityID: identityID, DeviceID: deviceID, ClusterID: "cluster-a",
		Namespace: "development", State: portForwardSessionActive, Generation: 1,
		NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	sessions, err := sessionapi.New(stateStore, sessionapi.Config{
		ClusterID: "cluster-a", Now: func() time.Time { return now }, Networks: crashNetworkDiscoverer{spec: network},
		Capabilities: crashCapabilityDiscoverer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	portForwardService, err := portforwardservice.New(
		stateStore,
		crashTargetResolver{},
		crashBindingManager{},
		portforwardservice.Config{
			Now: func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	policy := authorization.NewAuthenticated()
	apiServer, err := controlplane.NewServer(
		controlplane.Config{PublicURL: "http://127.0.0.1"},
		controlplane.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controlplane.WithAuthenticator(
			controlplaneapi.AuthenticatorFunc(
				func(request *http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
					if request.Header.Get("Authorization") != "Bearer crash-access" {
						return controlplaneapi.Identity{}, &controlplaneapi.Error{
							Code:    controlplaneapi.CodeUnauthenticated,
							Message: "invalid token",
						}
					}
					return controlplaneapi.Identity{Subject: identityID, DeviceID: deviceID}, nil
				},
			),
		),
		controlplane.WithAuthorizer(
			policy,
		),
		controlplane.WithAPIRoutes(
			controlplane.APIRoutes{PortForwards: portforwardapi.NewRoutes(portForwardService, sessions).Endpoints()},
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(apiServer.Handler())
	t.Cleanup(httpServer.Close)
	serverProfile := profile.Profile{ID: "server-1", BaseURL: httpServer.URL}
	credentialStore := &crashCredentialStore{
		profileID: serverProfile.ID,
		credential: credentials.Credential{
			TokenType: "Bearer", AccessToken: "crash-access", AccessExpiresAt: now.Add(time.Hour),
			RefreshToken: "unused", RefreshExpiresAt: now.Add(time.Hour), DeviceID: deviceID,
		},
	}
	remoteClient, err := remote.New(
		credentialStore,
		crashTokenRefresher{},
		remote.Config{Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := New(remoteClient, fakeDataPlane{})
	if err != nil {
		t.Fatal(err)
	}
	session := remote.Session{
		ID: sessionID, Namespace: "development", State: portForwardSessionActive, Generation: 1,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
		NetworkSpec: network, NetworkSpecHash: networkHash,
	}
	info, err := manager.Start(ctx, serverProfile, session, Request{
		ProfileID: serverProfile.ID, Kind: "service", Name: "api", Protocol: "tcp", RemotePort: 8080,
	})
	if err != nil {
		t.Fatal(err)
	}
	storedTask, err := stateStore.Tasks().GetByID(ctx, info.ID)
	if err != nil || storedTask.State != "running" {
		t.Fatalf("active remote Task = %#v, %v", storedTask, err)
	}

	// A process crash closes local file descriptors without issuing the remote
	// DELETE. Reproduce that boundary directly, then let Session TTL ownership
	// perform the only available server-side cleanup.
	manager.mu.Lock()
	entry := manager.active[info.ID]
	delete(manager.active, info.ID)
	manager.mu.Unlock()
	if entry == nil {
		t.Fatal("local Port Forward disappeared before crash simulation")
	}
	if err := manager.locals.Stop(entry.localID); err != nil {
		t.Fatal(err)
	}
	if _, err := stateStore.Tasks().GetByID(ctx, info.ID); err != nil {
		t.Fatalf("crash unexpectedly sent remote Task stop/delete: %v", err)
	}
	listener, err := net.Listen("tcp", info.Address)
	if err != nil {
		t.Fatalf("crash did not release local listener: %v", err)
	}
	_ = listener.Close()

	worker, err := maintenance.New(stateStore, slog.New(slog.NewTextHandler(io.Discard, nil)), maintenance.Config{
		Now: func() time.Time { return expiresAt.Add(time.Second) },
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Sessions != 1 {
		t.Fatalf("maintenance report = %#v", report)
	}
	if _, err := stateStore.Sessions().GetByID(ctx, sessionID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expired crashed Session lookup = %v", err)
	}
	if _, err := stateStore.Tasks().GetByID(ctx, info.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("crashed client's Port Forward Task lookup = %v", err)
	}
}
