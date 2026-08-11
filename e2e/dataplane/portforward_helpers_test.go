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

	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientportforward "github.com/fengqi-dev/kube-loop/internal/client/portforward"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/authorization"
	controllerkubernetes "github.com/fengqi-dev/kube-loop/internal/controller/kubernetes"
	"github.com/fengqi-dev/kube-loop/internal/controller/portforwardapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/capability"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"k8s.io/client-go/rest"
)

const portForwardAccessToken = "e2e-port-forward-access"

type staticNetworkDiscoverer struct {
	spec networkspec.Spec
}

type staticCapabilityDiscoverer struct{}

func (staticCapabilityDiscoverer) DiscoverCapabilities(
	context.Context,
	controller.Principal,
	string,
) (capability.Snapshot, *controller.APIError) {
	return capability.Snapshot{}, nil
}

type e2eBindingManager struct{}

func (e2eBindingManager) Activate(context.Context, sessionapi.ActiveSession, string, portforwardapi.Spec) (bool, error) {
	return true, nil
}

func (e2eBindingManager) Delete(context.Context, string, string) error { return nil }

func (discoverer staticNetworkDiscoverer) Discover(
	context.Context,
	controller.Principal,
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
	profile profile.Profile
	remote  *remote.Client
	store   *storage.Store
}

func startPortForwardControlPlane(
	t *testing.T,
	ctx context.Context,
	restConfig *rest.Config,
	gatewayAddress string,
	serverProfileID string,
	principalID string,
	deviceID string,
	session remote.Session,
) *portForwardControlPlane {
	t.Helper()
	provider, err := controllerkubernetes.NewForRESTConfig(restConfig, controllerkubernetes.Config{})
	if err != nil {
		t.Fatalf("create Port Forward Kubernetes Provider: %v", err)
	}
	resolver, err := portforwardapi.NewKubernetesResolver(provider)
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "port-forward-controller.db"),
	})
	if err != nil {
		t.Fatalf("open Port Forward Controller storage: %v", err)
	}
	t.Cleanup(func() {
		if err := stateStore.Close(); err != nil {
			t.Logf("close Port Forward Controller storage: %v", err)
		}
	})
	if _, err := stateStore.Principals().Upsert(ctx, storage.Principal{
		ID: principalID, Provider: "e2e", ExternalID: principalID,
		CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
	}); err != nil {
		t.Fatalf("create Port Forward principal: %v", err)
	}
	specJSON, err := networkspec.CanonicalJSON(session.NetworkSpec)
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Sessions().Create(ctx, storage.Session{
		ID: session.ID, PrincipalID: principalID, DeviceID: deviceID, ClusterID: "e2e",
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
	portForwards, err := portforwardapi.New(stateStore, sessions, resolver, e2eBindingManager{}, portforwardapi.Config{})
	if err != nil {
		t.Fatalf("create Port Forward API: %v", err)
	}
	router := controller.NewAPIRouter()
	for _, route := range []struct{ method, pattern string }{
		{http.MethodPost, "/api/v2/sessions/{sessionID}/port-forwards"},
		{http.MethodGet, "/api/v2/sessions/{sessionID}/port-forwards"},
		{http.MethodDelete, "/api/v2/sessions/{sessionID}/port-forwards/{taskID}"},
	} {
		if err := router.Handle(route.method, route.pattern, portForwards); err != nil {
			t.Fatalf("register Port Forward route: %v", err)
		}
	}
	policy, err := authorization.New(authorization.Policy{Rules: []authorization.Rule{{
		ID: "e2e-port-forward", Subjects: []string{principalID}, Namespaces: []string{session.Namespace},
		Operations: []string{"create", "list", "delete"}, ResourceKinds: []string{"port-forwards"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	controllerServer, err := controller.NewServer(
		controller.Config{PublicURL: "http://127.0.0.1"}, controller.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controller.WithAuthenticator(controller.AuthenticatorFunc(func(request *http.Request) (controller.Principal, *controller.APIError) {
			if request.Header.Get("Authorization") != "Bearer "+portForwardAccessToken {
				return controller.Principal{}, &controller.APIError{Code: controller.CodeUnauthenticated, Message: "invalid e2e access token"}
			}
			return controller.Principal{Subject: principalID, DeviceID: deviceID}, nil
		})),
		controller.WithAuthorizer(policy), controller.WithAPIHandler(router),
	)
	if err != nil {
		t.Fatalf("create Port Forward Controller: %v", err)
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
		profile: profile.Profile{ID: serverProfileID, BaseURL: publicServer.URL, TunnelPath: testPath},
		remote:  remoteClient,
		store:   stateStore,
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
