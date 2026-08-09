package session

import (
	"context"
	"errors"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
)

func TestLocalWebSocketEndpointStartsHTTPPortForward(t *testing.T) {
	provider := &localGatewayProvider{
		forwarder: &localGatewayForwarder{address: "127.0.0.1:43127"},
	}
	manager := &Manager{gateway: provider}

	endpoint, forwarder, err := manager.localWebSocketEndpoint(
		context.Background(), "minikube", "gateway-pod",
		"ws://127.0.0.1:8080/v1/tunnel",
	)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "ws://127.0.0.1:43127/v1/tunnel" {
		t.Fatalf("endpoint = %q", endpoint)
	}
	if forwarder != provider.forwarder {
		t.Fatal("local port-forward was not returned for lifecycle cleanup")
	}
	if provider.remotePort != cluster.GatewayHTTPPort {
		t.Fatalf("remote port = %d, want %d", provider.remotePort, cluster.GatewayHTTPPort)
	}
}

func TestLocalWebSocketEndpointLeavesIngressURLUnchanged(t *testing.T) {
	provider := &localGatewayProvider{}
	manager := &Manager{gateway: provider}
	want := "wss://gateway.example.com/v1/tunnel"

	endpoint, forwarder, err := manager.localWebSocketEndpoint(
		context.Background(), "minikube", "gateway-pod", want,
	)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != want || forwarder != nil {
		t.Fatalf("endpoint = %q, forwarder = %v", endpoint, forwarder)
	}
	if provider.calls != 0 {
		t.Fatalf("port-forward calls = %d, want 0", provider.calls)
	}
}

func TestGatewayEndpointForLogRemovesCredentialsAndQuery(t *testing.T) {
	got := gatewayEndpointForLog("wss://user:password@gateway.example.com/v1/tunnel?token=secret#debug")
	if want := "wss://gateway.example.com/v1/tunnel"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}

func TestCombinedGatewayForwarderClosesBothLayers(t *testing.T) {
	primaryErr := errors.New("primary")
	localErr := errors.New("local")
	primary := &localGatewayForwarder{address: "127.0.0.1:10001", closeErr: primaryErr}
	local := &localGatewayForwarder{address: "127.0.0.1:10002", closeErr: localErr}
	forwarder := &combinedGatewayForwarder{primary: primary, local: local}

	if forwarder.Address() != primary.address {
		t.Fatalf("address = %q", forwarder.Address())
	}
	err := forwarder.Close()
	if !errors.Is(err, primaryErr) || !errors.Is(err, localErr) {
		t.Fatalf("close error = %v", err)
	}
	if !primary.closed || !local.closed {
		t.Fatalf("closed primary=%t local=%t", primary.closed, local.closed)
	}
}

type localGatewayProvider struct {
	forwarder  *localGatewayForwarder
	remotePort uint16
	calls      int
}

func (p *localGatewayProvider) GetGateway(context.Context, string) (cluster.GatewayInfo, error) {
	return cluster.GatewayInfo{}, nil
}

func (p *localGatewayProvider) EnsureGateway(
	context.Context, string, string,
) (cluster.GatewayInfo, error) {
	return cluster.GatewayInfo{}, nil
}

func (p *localGatewayProvider) StartPortForward(
	_ context.Context, _, _ string, remotePort uint16,
) (cluster.PortForward, error) {
	p.calls++
	p.remotePort = remotePort
	if p.forwarder == nil {
		return nil, errors.New("unexpected port-forward")
	}
	return p.forwarder, nil
}

type localGatewayForwarder struct {
	address  string
	closed   bool
	closeErr error
}

func (f *localGatewayForwarder) Address() string { return f.address }

func (f *localGatewayForwarder) Close() error {
	f.closed = true
	return f.closeErr
}
