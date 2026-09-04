//go:build e2e

package dataplane

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/rest"

	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientportforward "github.com/fengqi-dev/kube-loop/internal/client/portforward"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanekubernetes "github.com/fengqi-dev/kube-loop/internal/controlplane/kubernetes"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/portforwardapi"
	portforwardservice "github.com/fengqi-dev/kube-loop/internal/controlplane/portforwardapi/service"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
	"github.com/fengqi-dev/kube-loop/internal/protocol/capability"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

const portForwardAccessToken = "e2e-port-forward-access"

type staticNetworkDiscoverer struct {
	spec networkspec.Spec
}

type staticCapabilityDiscoverer struct{}

func (staticCapabilityDiscoverer) DiscoverCapabilities(
	context.Context,
	controlplaneapi.Identity,
	string,
) (capability.Snapshot, *controlplaneapi.Error) {
	return capability.Snapshot{}, nil
}

func (discoverer staticNetworkDiscoverer) Discover(
	context.Context,
	controlplaneapi.Identity,
	string,
) (networkspec.Spec, error) {
	return discoverer.spec, nil
}

type e2eCredentialStore struct {
	mu         sync.Mutex
	profileID  string
	credential credentials.Credential
}

func (store *e2eCredentialStore) Set(profileID string, credential credentials.Credential) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if profileID != store.profileID {
		return errors.New("unknown e2e Server Profile")
	}
	store.credential = credential
	return nil
}

func (store *e2eCredentialStore) Get(profileID string) (credentials.Credential, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if profileID != store.profileID {
		return credentials.Credential{}, credentials.ErrNotFound
	}
	return store.credential, nil
}

func (store *e2eCredentialStore) Delete(profileID string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if profileID != store.profileID {
		return credentials.ErrNotFound
	}
	store.credential = credentials.Credential{}
	return nil
}

type e2eTokenRefresher struct{}

func (e2eTokenRefresher) Refresh(
	context.Context,
	string,
	credentials.Credential,
) (credentials.Credential, error) {
	return credentials.Credential{}, errors.New("unexpected e2e access token refresh")
}

type portForwardControlPlane struct {
	profile       profile.Profile
	remote        *remote.Client
	bindingConfig *rest.Config
}

func startPortForwardControlPlane(
	t *testing.T,
	ctx context.Context,
	restConfig *rest.Config,
	gatewayAddress string,
	serverProfileID string,
	identityID string,
	deviceID string,
	session remote.Session,
) *portForwardControlPlane {
	t.Helper()
	provider, err := controlplanekubernetes.NewForRESTConfig(restConfig, controlplanekubernetes.Config{})
	if err != nil {
		t.Fatalf("create Port Forward Kubernetes Provider: %v", err)
	}
	resolver, err := controlplanekubernetes.NewPortForwardResolver(provider)
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "port-forward-controlplane.db"),
	})
	if err != nil {
		t.Fatalf("open Port Forward Control Plane storage: %v", err)
	}
	t.Cleanup(func() {
		if err := stateStore.Close(); err != nil {
			t.Logf("close Port Forward Control Plane storage: %v", err)
		}
	})
	if _, err := stateStore.Identities().Create(ctx, storage.Identity{
		ID: identityID, Type: "human", DisplayName: "Test Identity", Status: "active",
		CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
	}); err != nil {
		t.Fatalf("create Port Forward identity: %v", err)
	}
	specJSON, err := networkspec.CanonicalJSON(session.NetworkSpec)
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Sessions().Create(ctx, storage.Session{
		ID: session.ID, IdentityID: identityID, DeviceID: deviceID, ClusterID: "e2e",
		Namespace: session.Namespace, State: "active", Generation: session.Generation,
		NetworkSpec: specJSON, NetworkSpecHash: session.NetworkSpecHash,
		CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
		LastHeartbeatAt: session.LastHeartbeatAt, ExpiresAt: session.ExpiresAt,
	}); err != nil {
		t.Fatalf("create Port Forward Session: %v", err)
	}
	sessions, err := sessionapi.New(stateStore, sessionapi.Config{
		ClusterID: "e2e", Networks: staticNetworkDiscoverer{spec: session.NetworkSpec},
		Capabilities: staticCapabilityDiscoverer{},
	})
	if err != nil {
		t.Fatalf("create Port Forward Session validator: %v", err)
	}
	bindingConfig, err := provider.SystemRESTConfig()
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := trafficbindingclient.NewForRESTConfig(bindingConfig, trafficbindingclient.Config{
		ControlPlaneID: e2eTrafficControlPlaneID,
	})
	if err != nil {
		t.Fatal(err)
	}
	bindingManager, err := portforwardapi.NewTrafficBindingManager(bindings)
	if err != nil {
		t.Fatal(err)
	}
	portForwards, err := portforwardservice.New(resolver, bindingManager, portforwardservice.Config{})
	if err != nil {
		t.Fatalf("create Port Forward API: %v", err)
	}
	policy := authorization.NewAuthenticated()
	controllerServer, err := controlplane.NewServer(
		controlplane.Config{PublicURL: "http://127.0.0.1"},
		controlplane.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controlplane.WithAuthenticator(
			controlplaneapi.AuthenticatorFunc(
				func(request *http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
					if request.Header.Get("Authorization") != "Bearer "+portForwardAccessToken {
						return controlplaneapi.Identity{}, &controlplaneapi.Error{
							Code:    controlplaneapi.CodeUnauthenticated,
							Message: "invalid e2e access token",
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
			controlplane.APIRoutes{PortForwards: portforwardapi.NewRoutes(portForwards, sessions).Endpoints()},
		),
	)
	if err != nil {
		t.Fatalf("create Port Forward Control Plane: %v", err)
	}
	gatewayURL, err := url.Parse("http://" + gatewayAddress)
	if err != nil {
		t.Fatal(err)
	}
	gatewayProxy := httputil.NewSingleHostReverseProxy(gatewayURL)
	publicServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == testPath {
			gatewayProxy.ServeHTTP(writer, request)
			return
		}
		controllerServer.Handler().ServeHTTP(writer, request)
	}))
	t.Cleanup(publicServer.Close)

	now := time.Now().UTC()
	credentialStore := &e2eCredentialStore{
		profileID: serverProfileID,
		credential: credentials.Credential{
			TokenType: "Bearer", AccessToken: portForwardAccessToken, AccessExpiresAt: now.Add(time.Hour),
			RefreshToken: "unused-e2e-refresh", RefreshExpiresAt: now.Add(time.Hour), DeviceID: deviceID,
		},
	}
	remoteClient, err := remote.New(credentialStore, e2eTokenRefresher{}, remote.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return &portForwardControlPlane{
		profile:       profile.Profile{ID: serverProfileID, BaseURL: publicServer.URL, TunnelPath: testPath},
		remote:        remoteClient,
		bindingConfig: bindingConfig,
	}
}

func assertPortForwardEcho(ctx context.Context, tcpAddress, udpAddress string) error {
	requestContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tcpConnection, err := (&net.Dialer{}).DialContext(requestContext, "tcp", tcpAddress)
	if err != nil {
		return fmt.Errorf("dial TCP Port Forward: %w", err)
	}
	defer tcpConnection.Close()
	_ = tcpConnection.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := tcpConnection.Write([]byte("hello")); err != nil {
		return fmt.Errorf("write TCP Port Forward: %w", err)
	}
	tcpResponse := make([]byte, len("tcp:hello"))
	if _, err := io.ReadFull(tcpConnection, tcpResponse); err != nil {
		return fmt.Errorf("read TCP Port Forward: %w", err)
	}
	if string(tcpResponse) != "tcp:hello" {
		return fmt.Errorf("unexpected TCP Port Forward response %q", tcpResponse)
	}

	udpConnection, err := (&net.Dialer{}).DialContext(requestContext, "udp", udpAddress)
	if err != nil {
		return fmt.Errorf("dial UDP Port Forward: %w", err)
	}
	defer udpConnection.Close()
	_ = udpConnection.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := udpConnection.Write([]byte("hello")); err != nil {
		return fmt.Errorf("write UDP Port Forward: %w", err)
	}
	udpResponse := make([]byte, 64)
	count, err := udpConnection.Read(udpResponse)
	if err != nil {
		return fmt.Errorf("read UDP Port Forward: %w", err)
	}
	if string(udpResponse[:count]) != "udp:hello" {
		return fmt.Errorf("unexpected UDP Port Forward response %q", udpResponse[:count])
	}
	return nil
}

func availablePortForwardPort(t *testing.T, network string) uint16 {
	t.Helper()
	reserve := utils.FreeUDPPort
	if network == "tcp" {
		reserve = utils.FreeTCPPort
	}
	port, err := reserve()
	if err != nil {
		t.Fatalf("reserve %s Port Forward port: %v", network, err)
	}
	return uint16(port)
}

func assertPortForwardAddresses(
	t *testing.T,
	manager *clientportforward.Manager,
	profileID string,
	want ...clientportforward.Info,
) {
	t.Helper()
	items := manager.List(profileID)
	if len(items) != len(want) {
		t.Fatalf("active Port Forwards = %#v, want %d", items, len(want))
	}
	byID := make(map[string]clientportforward.Info, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	for _, expected := range want {
		item, ok := byID[expected.ID]
		if !ok || item.Address != expected.Address || item.LocalPort != expected.LocalPort {
			t.Fatalf("Port Forward %s changed local listener: got %#v want %#v", expected.ID, item, expected)
		}
	}
}

func assertPortForwardPortsReleased(t *testing.T, tcpAddress, udpAddress string) {
	t.Helper()
	tcpListener, err := net.Listen("tcp", tcpAddress)
	if err != nil {
		t.Fatalf("TCP Port Forward listener was not released: %v", err)
	}
	_ = tcpListener.Close()
	udpListener, err := net.ListenPacket("udp", udpAddress)
	if err != nil {
		t.Fatalf("UDP Port Forward listener was not released: %v", err)
	}
	_ = udpListener.Close()
}
