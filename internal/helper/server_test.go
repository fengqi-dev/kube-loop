package helper

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/helperrpc"
	"github.com/fengqi-dev/kube-loop/internal/protocol/sessionspec"
)

func TestServerShutdownClosesAndWaitsForHandlers(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(AuthFile{})
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- server.serve(ctx, listener) }()

	connection, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		server.connectionMu.Lock()
		active := len(server.connections)
		server.connectionMu.Unlock()
		if active == 1 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("helper handler did not accept connection")
		}
		time.Sleep(time.Millisecond)
	}

	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not wait for blocked handler shutdown")
	}
	server.connectionMu.Lock()
	active := len(server.connections)
	server.connectionMu.Unlock()
	if active != 0 {
		t.Fatalf("active helper connections=%d", active)
	}
}

func TestDispatchRejectsLegacyExecutableRequest(t *testing.T) {
	server := NewServer(AuthFile{Token: "secret"})
	response := server.dispatch(helperrpc.Request{Op: helperrpc.OpStart, Token: "secret"})
	if response.OK || response.Error != "session is required" {
		t.Fatalf("dispatch() = %#v", response)
	}
}

func TestDispatchRequiresValidSessionIDForStop(t *testing.T) {
	server := NewServer(AuthFile{Token: "secret"})
	response := server.dispatch(helperrpc.Request{
		Op: helperrpc.OpStop, Token: "secret", SessionID: "../../session",
	})
	if response.OK {
		t.Fatalf("dispatch() unexpectedly accepted an unsafe session ID")
	}
}

func TestStartSessionRejectsServerClosing(t *testing.T) {
	server := NewServer(AuthFile{})
	server.closing.Store(true)
	if err := server.startSession(sessionspec.Spec{}); !errors.Is(err, errServerClosing) {
		t.Fatalf("startSession error = %v, want %v", err, errServerClosing)
	}
}

func TestTailText(t *testing.T) {
	if got := tailText([]byte("short"), 8); got != "short" {
		t.Fatalf("short tail = %q", got)
	}
	if got := tailText([]byte("0123456789"), 4); got != "6789" {
		t.Fatalf("truncated tail = %q", got)
	}
	if got := tailText([]byte("0123456789"), 0); got != "0123456789" {
		t.Fatalf("unlimited tail = %q", got)
	}
}
