package gateway

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

func TestClusterAddressPolicy(t *testing.T) {
	allowed := []string{"10.0.0.1", "172.16.1.2", "192.168.10.2", "fd00::1"}
	for _, raw := range allowed {
		if !isClusterAddress(netip.MustParseAddr(raw)) {
			t.Errorf("expected %s to be allowed", raw)
		}
	}
	denied := []string{"127.0.0.1", "8.8.8.8", "169.254.1.1", "::1"}
	for _, raw := range denied {
		if isClusterAddress(netip.MustParseAddr(raw)) {
			t.Errorf("expected %s to be denied", raw)
		}
	}
}

func TestDrainWaitsForActiveConnection(t *testing.T) {
	server := NewServer(nil, time.Second)
	client, gatewayConnection := net.Pipe()
	defer client.Close()
	go server.ServeConnForAuthorization(gatewayConnection, gatewayTestAuthorization(t))
	waitForActiveConnections(t, server, 1)

	drainResult := make(chan error, 1)
	go func() { drainResult <- server.Drain(context.Background()) }()
	select {
	case err := <-drainResult:
		t.Fatalf("Drain returned before the active connection closed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	_ = client.Close()
	select {
	case err := <-drainResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Drain did not finish after the active connection closed")
	}
}

func TestDrainDeadlineClosesActiveConnections(t *testing.T) {
	server := NewServer(nil, time.Second)
	client, gatewayConnection := net.Pipe()
	defer client.Close()
	go server.ServeConnForAuthorization(gatewayConnection, gatewayTestAuthorization(t))
	waitForActiveConnections(t, server, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := server.Drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain error = %v", err)
	}
	if active := server.ActiveConnections(); active != 0 {
		t.Fatalf("active connections = %d", active)
	}
}

func TestBeginDrainRejectsNewConnections(t *testing.T) {
	server := NewServer(nil, time.Second)
	server.BeginDrain()
	client, gatewayConnection := net.Pipe()
	authorization := gatewayTestAuthorization(t)
	done := make(chan struct{})
	go func() {
		server.ServeConnForAuthorization(gatewayConnection, authorization)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ServeConn did not reject a connection while draining")
	}
	if !server.Draining() || server.ActiveConnections() != 0 {
		t.Fatalf("draining = %t, active = %d", server.Draining(), server.ActiveConnections())
	}
	_ = client.Close()
}

func gatewayTestAuthorization(t *testing.T) SessionAuthorization {
	t.Helper()
	_, specHash := gatewayTestNetworkSpec(t)
	return SessionAuthorization{
		SessionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Generation: 1,
		Namespace: "default", NetworkSpecHash: specHash,
	}
}

func TestAuthenticatedWebSocketSessionRejectsMismatchedProtocolTenant(t *testing.T) {
	server := NewServer(nil, time.Second)
	client, gatewayConnection := net.Pipe()
	done := make(chan struct{})
	spec, specHash := gatewayTestNetworkSpec(t)
	go func() {
		server.ServeConnForAuthorization(gatewayConnection, SessionAuthorization{
			SessionID: "33333333-3333-4333-8333-333333333333", Generation: 1,
			Namespace: "development", NetworkSpecHash: specHash,
		})
		close(done)
	}()
	wrongToken, err := tunnel.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan error, 1)
	go func() { writeDone <- tunnel.WriteAuthorizedControlSession(client, wrongToken, spec) }()
	if err := tunnel.ReadStatus(client); err == nil || !strings.Contains(err.Error(), "does not match RelayTicket") {
		t.Fatalf("status error = %v", err)
	}
	if err := <-writeDone; err != nil && !errors.Is(err, net.ErrClosed) {
		t.Logf("control writer closed after rejection: %v", err)
	}
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("authenticated connection did not close after tenant mismatch")
	}
}

func TestAuthenticatedWebSocketSessionAcceptsBoundProtocolTenant(t *testing.T) {
	server := NewServer(nil, time.Second)
	client, gatewayConnection := net.Pipe()
	done := make(chan struct{})
	const sessionID = "33333333-3333-4333-8333-333333333333"
	spec, specHash := gatewayTestNetworkSpec(t)
	go func() {
		server.ServeConnForAuthorization(gatewayConnection, SessionAuthorization{
			SessionID: sessionID, Generation: 1, Namespace: "development", NetworkSpecHash: specHash,
		})
		close(done)
	}()
	token, err := tunnel.RelaySessionToken(sessionID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := tunnel.WriteAuthorizedControlSession(client, token, spec); err != nil {
		t.Fatal(err)
	}
	if err := tunnel.ReadStatus(client); err != nil {
		t.Fatalf("control status = %v", err)
	}
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("authenticated connection did not close")
	}
}

func TestAuthenticatedSessionDeniesTargetOutsideRegisteredNetworkSpec(t *testing.T) {
	server := NewServer(nil, time.Second)
	const sessionID = "33333333-3333-4333-8333-333333333333"
	spec, specHash := gatewayTestNetworkSpec(t)
	authorization := SessionAuthorization{
		SessionID: sessionID, Generation: 1, Namespace: "development", NetworkSpecHash: specHash,
	}
	token, err := tunnel.RelaySessionToken(sessionID, 1)
	if err != nil {
		t.Fatal(err)
	}
	control, gatewayControl := net.Pipe()
	go server.ServeConnForAuthorization(gatewayControl, authorization)
	if err := tunnel.WriteAuthorizedControlSession(control, token, spec); err != nil {
		t.Fatal(err)
	}
	if err := tunnel.ReadStatus(control); err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	client, gatewayConnection := net.Pipe()
	go server.ServeConnForAuthorization(gatewayConnection, authorization)
	if err := tunnel.WriteOpen(client, tunnel.OpenRequest{
		Command: tunnel.CommandTCP, Host: "10.96.0.1", Port: 443,
	}, token); err != nil {
		t.Fatal(err)
	}
	if err := tunnel.ReadStatus(client); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("target status = %v", err)
	}
	_ = client.Close()
}

func TestAuthenticatedControlRejectsNetworkSpecHashMismatch(t *testing.T) {
	server := NewServer(nil, time.Second)
	const sessionID = "33333333-3333-4333-8333-333333333333"
	spec, _ := gatewayTestNetworkSpec(t)
	token, err := tunnel.RelaySessionToken(sessionID, 1)
	if err != nil {
		t.Fatal(err)
	}
	client, gatewayConnection := net.Pipe()
	go server.ServeConnForAuthorization(gatewayConnection, SessionAuthorization{
		SessionID: sessionID, Generation: 1, Namespace: "development",
		NetworkSpecHash: strings.Repeat("f", 64),
	})
	if err := tunnel.WriteAuthorizedControlSession(client, token, spec); err != nil {
		t.Fatal(err)
	}
	if err := tunnel.ReadStatus(client); err == nil || !strings.Contains(err.Error(), "does not match RelayTicket") {
		t.Fatalf("control status = %v", err)
	}
	_ = client.Close()
}

func gatewayTestNetworkSpec(t *testing.T) (networkspec.Spec, string) {
	t.Helper()
	spec, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.2.0.0/16"}, ServiceCIDRs: []string{"10.96.0.0/12"},
		PodIPs: []string{"10.2.1.7"}, ServiceIPs: []string{"10.96.1.20"}, DNSServer: "10.96.0.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := networkspec.Hash(spec)
	if err != nil {
		t.Fatal(err)
	}
	return spec, hash
}

func waitForActiveConnections(t *testing.T, server *Server, expected int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if server.ActiveConnections() == expected {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("active connections = %d, want %d", server.ActiveConnections(), expected)
}

func TestResolvePrivateRejectsPublicTarget(t *testing.T) {
	if _, err := resolvePrivate(context.Background(), "8.8.8.8", 53); err == nil {
		t.Fatal("expected public target to be rejected")
	}
}
