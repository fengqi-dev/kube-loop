package listener

import (
	"context"
	"net"
	"testing"
)

type testForwarder struct {
	address string
}

func (f *testForwarder) Address() string { return f.address }
func (f *testForwarder) Close() error    { return nil }

func TestConnectivityDialsActiveTCPForward(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	manager := NewManager()
	manager.active["pf-1"] = &runtimeForward{
		info: Info{
			ID: "pf-1", Protocol: "tcp", Address: listener.Addr().String(),
		},
		forwarder: &testForwarder{address: listener.Addr().String()},
	}
	if err := manager.Test(context.Background(), "pf-1"); err != nil {
		t.Fatal(err)
	}
}

func TestConnectivityRejectsMissingForward(t *testing.T) {
	manager := NewManager()
	if err := manager.Test(context.Background(), "missing"); err == nil {
		t.Fatal("expected missing port-forward error")
	}
}

func TestConnectivityRejectsUDPForward(t *testing.T) {
	manager := NewManager()
	manager.active["pf-1"] = &runtimeForward{
		info: Info{ID: "pf-1", Protocol: "udp", Address: "127.0.0.1:12345"},
	}
	if err := manager.Test(context.Background(), "pf-1"); err == nil {
		t.Fatal("expected unsupported UDP test error")
	}
}
