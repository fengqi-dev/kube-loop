package dataplane

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/socksbridge"
	"github.com/fengqi-dev/kube-loop/internal/client/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

func TestRuntimeConfigUsesProfileSOCKSPort(t *testing.T) {
	base := Config{ListenAddress: "127.0.0.1:1080"}
	if got := runtimeConfig(base, profile.Profile{}).ListenAddress; got != base.ListenAddress {
		t.Fatalf("default listen address = %q", got)
	}
	if got := runtimeConfig(base, profile.Profile{SOCKSPort: 2080}).ListenAddress; got != "127.0.0.1:2080" {
		t.Fatalf("profile listen address = %q", got)
	}
}

func TestModeValidationAndInvalidSwitches(t *testing.T) {
	for _, mode := range []Mode{ModeSOCKS, ModeTUN} {
		if err := mode.Validate(); err != nil {
			t.Fatalf("mode %q validation error = %v", mode, err)
		}
	}
	invalid := Mode("invalid")
	if err := invalid.Validate(); err == nil {
		t.Fatal("invalid Data Plane mode was accepted")
	}
	manager := &Manager{}
	if _, err := manager.SwitchMode(t.Context(), "server", invalid); err == nil {
		t.Fatal("invalid SwitchMode was accepted")
	}
	if _, err := manager.ConnectMode(
		t.Context(),
		profile.Profile{ID: "server"},
		remote.Session{},
		invalid,
	); err == nil {
		t.Fatal("invalid ConnectMode was accepted")
	}
}

func TestManagerThinOperationsRequireConnectedRuntime(t *testing.T) {
	manager := &Manager{active: map[string]*managedRuntime{}}
	if err := manager.TestConnectivity(t.Context(), "missing"); err == nil {
		t.Fatal("TestConnectivity accepted a missing Runtime")
	}
	if _, err := manager.Logs(t.Context(), "missing"); err == nil {
		t.Fatal("Logs accepted a missing Runtime")
	}
	if _, err := manager.ConfigJSON("missing"); err == nil {
		t.Fatal("ConfigJSON accepted a missing Runtime")
	}
	if err := manager.UpdateDNSNamespace(t.Context(), "missing", "default"); err == nil {
		t.Fatal("UpdateDNSNamespace accepted a missing Runtime")
	}
	if err := manager.UpdateHostAliases(t.Context(), "missing", nil); err == nil {
		t.Fatal("UpdateHostAliases accepted a missing Runtime")
	}
	if _, err := manager.Dialer("missing"); err == nil {
		t.Fatal("Dialer accepted a missing Runtime")
	}
}

func TestManagerSetHostTCPHandlerUpdatesActiveRuntime(t *testing.T) {
	bridge := &testBridge{}
	runtime := &Runtime{bridge: bridge, status: Status{Mode: ModeTUN}}
	manager := &Manager{
		active:  map[string]*managedRuntime{"server": {runtime: runtime}},
		hostTCP: map[string]socksbridge.HostTCPHandler{},
	}
	handler := func(string, uint16) (func(net.Conn), bool) { return nil, false }
	if err := manager.SetHostTCPHandler(" server ", handler); err != nil {
		t.Fatal(err)
	}
	bridge.mu.Lock()
	installed := bridge.hostTCP != nil
	bridge.mu.Unlock()
	if !installed || manager.hostTCP["server"] == nil {
		t.Fatal("Host TCP handler was not installed on Manager and Runtime")
	}
	if err := manager.SetHostTCPHandler("missing", handler); err == nil {
		t.Fatal("Host TCP handler accepted a missing Runtime")
	}
}

func TestSlowTUNStartDoesNotBlockAnotherProfileStatus(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{PodCIDRs: []string{"10.42.0.0/16"}})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := networkspec.Hash(spec)
	if err != nil {
		t.Fatal(err)
	}
	starter := &blockingTUNStarter{
		testTUNStarter: &testTUNStarter{},
		started:        make(chan struct{}),
		release:        make(chan struct{}),
	}
	t.Cleanup(starter.unblock)
	var starts atomic.Int32
	manager, err := NewManager(&testTickets{}, Config{
		TUNStarter: starter,
		startForwarder: func(ctx context.Context, clientConfig websocketmux.ClientConfig) (streamForwarder, error) {
			if _, err := clientConfig.TokenSource(ctx); err != nil {
				return nil, err
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return nil, err
			}
			go acceptTestControl(listener)
			return &testForwarder{Listener: listener}, nil
		},
		listenSOCKS: func(context.Context, string) (localBridge, error) {
			port := 49000 + starts.Add(1)
			return &testBridge{address: testAddress("127.0.0.1:" + strconv.Itoa(int(port)))}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	profiles := []profile.Profile{
		{ID: "service-slow", BaseURL: "https://gateway.example.test", TunnelPath: defaultTunnelPath},
		{ID: "service-independent", BaseURL: "https://gateway.example.test", TunnelPath: defaultTunnelPath},
	}
	for index, serverProfile := range profiles {
		session := remote.Session{
			ID: uuid.NewString(), Namespace: "development", State: dataplaneSessionActive,
			Generation: uint64(index + 1), NetworkSpec: spec, NetworkSpecHash: hash,
		}
		if _, err := manager.Connect(context.Background(), serverProfile, session); err != nil {
			t.Fatal(err)
		}
	}
	tunResult := make(chan error, 1)
	go func() {
		_, startErr := manager.StartTUN(context.Background(), profiles[0].ID)
		tunResult <- startErr
	}()
	select {
	case <-starter.started:
	case <-time.After(time.Second):
		t.Fatal("TUN start did not begin")
	}
	statusResult := make(chan error, 1)
	go func() {
		_, statusErr := manager.Status(profiles[1].ID)
		statusResult <- statusErr
	}()
	select {
	case err := <-statusResult:
		if err != nil {
			t.Fatalf("independent Status failed: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("independent Status blocked on another profile's TUN startup")
	}
	starter.unblock()
	if err := <-tunResult; err != nil {
		t.Fatal(err)
	}
}

func TestManagerShutdownWaitsForStatusCallbackAndRejectsConnect(t *testing.T) {
	callbackStarted := make(chan struct{})
	releaseCallback := make(chan struct{})
	var callbackOnce sync.Once
	manager, err := NewManager(&testTickets{}, Config{OnStatus: func(StatusEvent) {
		callbackOnce.Do(func() { close(callbackStarted) })
		<-releaseCallback
	}})
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCallback) }) }
	t.Cleanup(release)
	manager.emit("server", Status{State: dataplaneConnected}, nil)
	select {
	case <-callbackStarted:
	case <-time.After(time.Second):
		t.Fatal("Data Plane status callback did not start")
	}
	shutdown := make(chan error, 1)
	go func() { shutdown <- manager.Shutdown() }()
	select {
	case err := <-shutdown:
		t.Fatalf("Shutdown returned before status callback completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if err := <-shutdown; err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Connect(
		t.Context(),
		profile.Profile{ID: "server"},
		remote.Session{State: dataplaneSessionActive},
	); !errors.Is(err, ErrClosed) {
		t.Fatalf("Connect after Shutdown error = %v, want ErrClosed", err)
	}
}

func TestManagerOpenTrafficStreamRequiresMatchingActiveRuntimeSession(t *testing.T) {
	session := remote.Session{ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", State: dataplaneSessionActive, Generation: 1}
	runtime := &Runtime{ctx: context.Background(), status: Status{
		State: dataplaneConnected, SessionID: session.ID, SessionGeneration: session.Generation,
	}}
	entry := &managedRuntime{session: session, runtime: runtime}
	manager := &Manager{active: map[string]*managedRuntime{"server": entry}}

	//nolint:staticcheck // This test intentionally verifies defensive rejection of a nil context.
	if _, err := manager.OpenTrafficStream(nil, "server", tunnel.TrafficModeExchange, "task"); err == nil {
		t.Fatal("nil Traffic stream context was accepted")
	}
	if _, err := manager.OpenTrafficStream(t.Context(), "other", tunnel.TrafficModeExchange, "task"); err == nil {
		t.Fatal("missing Data Plane runtime was accepted")
	}
	entry.recovering = true
	if _, err := manager.OpenTrafficStream(t.Context(), "server", tunnel.TrafficModeExchange, "task"); err == nil {
		t.Fatal("recovering Data Plane runtime was accepted")
	}
	entry.recovering = false
	runtime.status.SessionGeneration++
	if _, err := manager.OpenTrafficStream(t.Context(), "server", tunnel.TrafficModeExchange, "task"); err == nil ||
		!strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched Data Plane Session error = %v", err)
	}
	runtime.status.SessionGeneration = session.Generation
	if _, err := manager.OpenTrafficStream(t.Context(), "server", tunnel.TrafficModeExchange, "task"); err == nil ||
		!strings.Contains(err.Error(), "not connected") {
		t.Fatalf("missing current transport error = %v", err)
	}
}
