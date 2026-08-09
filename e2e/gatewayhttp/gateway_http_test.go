//go:build e2e

package gatewayhttp

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/e2e/harness"
	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/tunnel"
)

const (
	testGatewayName  = "kubeloop-gateway-e2e-http"
	testGatewayToken = "kubeloop-e2e-http-token"
	testStreamCount  = 12
)

func TestMain(m *testing.M) { harness.RunMain(m) }

func TestHTTPGatewayMultiplexesClusterTCPStreams(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := harness.TestContext(t, 3*time.Minute)
	defer cancel()

	provider := harness.NewProvider(t)
	provider.SetGatewayResource(cluster.GatewayNamespace, testGatewayName)
	client := harness.KubeClient(t, provider)
	if err := harness.EnsureEchoWorkload(ctx, client); err != nil {
		t.Fatalf("ensure echo workload: %v", err)
	}

	externalEndpoint := strings.TrimSpace(os.Getenv("KUBELOOP_E2E_GATEWAY_WS_URL"))
	installEndpoint := externalEndpoint
	if installEndpoint == "" {
		installEndpoint = "ws://127.0.0.1:8080/v1/tunnel"
	}
	gateway, err := provider.EnsureHTTPGateway(
		ctx, harness.KubeContext(), harness.GatewayImage(), testGatewayToken,
		installEndpoint,
	)
	if err != nil {
		t.Fatalf("ensure HTTP gateway: %v", err)
	}
	endpoint := gatewayEndpoint(t, ctx, provider, gateway, externalEndpoint)

	t.Run("rejects invalid bearer token", func(t *testing.T) {
		forwarder, err := websocketmux.Start(ctx, websocketmux.ClientConfig{
			URL: endpoint, Token: testGatewayToken + "-invalid", PoolSize: 1,
		})
		if forwarder != nil {
			_ = forwarder.Close()
			t.Fatal("HTTP gateway accepted an invalid bearer token")
		}
		if err == nil || !strings.Contains(err.Error(), "401") {
			t.Fatalf("invalid bearer token error = %v, want HTTP 401", err)
		}
	})

	forwarder, err := websocketmux.Start(ctx, websocketmux.ClientConfig{
		URL:               endpoint,
		Token:             testGatewayToken,
		PoolSize:          2,
		MaxPhysical:       4,
		MaxStreamsPerConn: 4,
	})
	if err != nil {
		t.Fatalf("start WebSocket multiplexer: %v", err)
	}
	t.Cleanup(func() {
		if err := forwarder.Close(); err != nil {
			t.Logf("close WebSocket multiplexer: %v", err)
		}
	})

	sessionToken, err := tunnel.NewSessionToken()
	if err != nil {
		t.Fatalf("create tunnel session token: %v", err)
	}
	control := dialControl(t, ctx, forwarder.Address(), sessionToken)
	t.Cleanup(func() { _ = control.Close() })

	clusterIP := harness.EchoServiceIP(t, ctx, client)
	streams := make([]net.Conn, 0, testStreamCount)
	for index := range testStreamCount {
		stream, err := dialClusterStream(
			ctx, forwarder.Address(), sessionToken, clusterIP, 8080,
		)
		if err != nil {
			closeConnections(streams)
			t.Fatalf("open multiplexed stream %d: %v", index, err)
		}
		streams = append(streams, stream)
	}
	t.Cleanup(func() { closeConnections(streams) })

	errors := make(chan error, len(streams))
	for index, stream := range streams {
		go func(index int, stream net.Conn) {
			payload := fmt.Sprintf("mux-%02d", index)
			want := "cluster-tcp:" + payload
			if _, err := io.WriteString(stream, payload); err != nil {
				errors <- fmt.Errorf("stream %d write: %w", index, err)
				return
			}
			response := make([]byte, len(want))
			if _, err := io.ReadFull(stream, response); err != nil {
				errors <- fmt.Errorf("stream %d read: %w", index, err)
				return
			}
			if string(response) != want {
				errors <- fmt.Errorf("stream %d response = %q, want %q", index, response, want)
				return
			}
			errors <- nil
		}(index, stream)
	}
	for range streams {
		if err := <-errors; err != nil {
			t.Error(err)
		}
	}
}

func gatewayEndpoint(
	t *testing.T,
	ctx context.Context,
	provider *cluster.Provider,
	gateway cluster.GatewayInfo,
	externalEndpoint string,
) string {
	t.Helper()
	if externalEndpoint != "" {
		return externalEndpoint
	}
	forwarder, err := provider.StartPortForward(
		ctx, harness.KubeContext(), gateway.Name, cluster.GatewayHTTPPort,
	)
	if err != nil {
		t.Fatalf("HTTP gateway port-forward: %v", err)
	}
	t.Cleanup(func() {
		if err := forwarder.Close(); err != nil {
			t.Logf("close HTTP gateway port-forward: %v", err)
		}
	})
	return "ws://" + forwarder.Address() + websocketmux.DefaultPath
}

func dialControl(
	t *testing.T, ctx context.Context, address string, token tunnel.SessionToken,
) net.Conn {
	t.Helper()
	conn, err := dialWithDeadline(ctx, address)
	if err != nil {
		t.Fatalf("dial control stream: %v", err)
	}
	err = tunnel.WriteControlSession(conn, token)
	if err == nil {
		err = tunnel.ReadStatus(conn)
	}
	if err != nil {
		_ = conn.Close()
		t.Fatalf("activate tunnel session: %v", err)
	}
	return conn
}

func dialClusterStream(
	ctx context.Context, address string, token tunnel.SessionToken, host string, port uint16,
) (net.Conn, error) {
	conn, err := dialWithDeadline(ctx, address)
	if err != nil {
		return nil, err
	}
	request := tunnel.OpenRequest{Command: tunnel.CommandTCP, Host: host, Port: port}
	err = tunnel.WriteOpen(conn, request, token)
	if err == nil {
		err = tunnel.ReadStatus(conn)
	}
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func dialWithDeadline(ctx context.Context, address string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(30 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func closeConnections(connections []net.Conn) {
	for _, connection := range connections {
		_ = connection.Close()
	}
}
