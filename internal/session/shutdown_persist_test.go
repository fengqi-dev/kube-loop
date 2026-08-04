package session

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/portfwd"
	"github.com/fengqi-dev/kube-loop/internal/store"
)

func TestShutdownPreservesRoutedPortForwardIntents(t *testing.T) {
	stateStore, err := store.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{discovery: cluster.Discovery{
		PodCIDRs: []string{"10.244.0.0/16"}, ServiceIPs: []string{"10.96.0.1"},
	}}
	manager := NewManager(
		provider,
		WithStore(stateStore),
		WithCore(newFakeCore()),
		WithBridgeFactory(testBridge),
	)
	connected := make(chan struct{}, 1)
	manager.Subscribe(func(state State) {
		if state.Phase == PhaseConnected {
			select {
			case connected <- struct{}{}:
			default:
			}
		}
	})
	if err := manager.Connect(context.Background(), Request{
		Context: "dev", Namespace: "default",
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for connected state; current state: %#v", manager.State())
	}

	if _, err := manager.StartPortForwardSession(context.Background(), portfwd.Request{
		Context:    "dev",
		Namespace:  "default",
		Kind:       portfwd.KindService,
		Name:       "api",
		Protocol:   "tcp",
		RemotePort: 80,
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Shutdown(); err != nil {
		t.Fatal(err)
	}

	clusterState := stateStore.Cluster("dev")
	if len(clusterState.PortForwards) != 1 {
		t.Fatalf("shutdown erased port-forward restore intents: %#v", clusterState.PortForwards)
	}
	if got := clusterState.PortForwards[0]; got.Kind != portfwd.KindService || got.Name != "api" {
		t.Fatalf("unexpected persisted port-forward: %#v", got)
	}
	if !clusterState.Connected {
		t.Fatal("shutdown cleared the auto-reconnect flag")
	}
}

func TestConnectedStatePublishesAfterBindingsAreRestored(t *testing.T) {
	stateStore, err := store.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.SetPortForwards("dev", []store.PortForwardSpec{{
		Namespace:  "default",
		Kind:       portfwd.KindService,
		Name:       "api",
		Protocol:   "tcp",
		RemotePort: 80,
	}}); err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{discovery: cluster.Discovery{
		PodCIDRs: []string{"10.244.0.0/16"}, ServiceIPs: []string{"10.96.0.1"},
	}}
	manager := NewManager(
		provider,
		WithStore(stateStore),
		WithCore(newFakeCore()),
		WithBridgeFactory(testBridge),
	)
	restoredCount := make(chan int, 1)
	manager.Subscribe(func(state State) {
		if state.Phase == PhaseConnected {
			select {
			case restoredCount <- len(manager.ListPortForwards()):
			default:
			}
		}
	})
	if err := manager.Connect(context.Background(), Request{
		Context: "dev", Namespace: "default",
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case count := <-restoredCount:
		if count != 1 {
			t.Fatalf("connected state published before bindings were restored: count=%d", count)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for connected state; current state: %#v", manager.State())
	}
	if err := manager.Shutdown(); err != nil {
		t.Fatal(err)
	}
}

func TestSOCKSConnectedStatePublishesAfterNativePortForwardsAreRestored(t *testing.T) {
	stateStore, err := store.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.SetPortForwards("dev", []store.PortForwardSpec{{
		Namespace:  "default",
		Kind:       portfwd.KindService,
		Name:       "api",
		Protocol:   "tcp",
		RemotePort: 80,
	}}); err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{discovery: cluster.Discovery{
		PodCIDRs: []string{"10.244.0.0/16"}, ServiceIPs: []string{"10.96.0.1"},
	}}
	manager := NewManager(
		provider,
		WithStore(stateStore),
		WithCore(newFakeCore()),
		WithBridgeFactory(testBridge),
	)
	restored := make(chan []portfwd.Info, 1)
	manager.Subscribe(func(state State) {
		if state.Phase == PhaseConnected {
			select {
			case restored <- manager.ListPortForwards():
			default:
			}
		}
	})
	if err := manager.Connect(context.Background(), Request{
		Context: "dev", Namespace: "default", Mode: ConnectionModeSOCKS,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case items := <-restored:
		if len(items) != 1 {
			t.Fatalf("SOCKS connected before port-forward restore: count=%d", len(items))
		}
		if items[0].PodName != "api-0" {
			t.Fatalf("SOCKS port-forward did not use native pod forwarding: %#v", items[0])
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for SOCKS connected state; current state: %#v", manager.State())
	}
	if err := manager.Shutdown(); err != nil {
		t.Fatal(err)
	}
}
