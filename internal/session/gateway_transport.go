package session

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/store"
)

const (
	GatewayTransportPortForward = "port-forward"
	GatewayTransportWebSocket   = "websocket"
)

func (m *Manager) GatewayTransport(contextName string) store.GatewayTransport {
	if m.store == nil {
		return store.GatewayTransport{Mode: GatewayTransportPortForward}
	}
	return m.store.GatewayTransport(contextName)
}

func (m *Manager) startGatewayForwarder(
	ctx context.Context, contextName string, gateway cluster.GatewayInfo,
) (cluster.PortForward, string, error) {
	config := m.GatewayTransport(contextName)
	if config.Mode != GatewayTransportWebSocket {
		m.AppendLog("INFO", fmt.Sprintf(
			"starting Gateway port-forward: pod=%s remote_port=%d",
			gateway.Name, cluster.GatewayPort,
		))
		forwarder, err := m.gateway.StartPortForward(ctx, contextName, gateway.Name, cluster.GatewayPort)
		if err == nil {
			m.AppendLog("INFO", "Gateway port-forward ready: local="+forwarder.Address())
		}
		return forwarder, "Gateway port-forward", err
	}
	endpoint, localForwarder, err := m.localWebSocketEndpoint(
		ctx, contextName, gateway.Name, config.URL,
	)
	if err != nil {
		return nil, "Gateway WebSocket local port-forward", err
	}
	if localForwarder != nil {
		m.AppendLog("INFO", fmt.Sprintf(
			"local HTTP Gateway port-forward ready: pod=%s remote_port=%d local=%s",
			gateway.Name, cluster.GatewayHTTPPort, localForwarder.Address(),
		))
	}
	poolSize, maxPhysical, maxStreams := effectiveMultiplexingLimits(config)
	tlsVerification := "enabled"
	if config.InsecureSkipVerify {
		tlsVerification = "disabled"
	}
	m.AppendLog("INFO", fmt.Sprintf(
		"starting Gateway WebSocket multiplexer: upstream=%s pool=%d max_physical=%d max_streams=%d tls_verification=%s",
		gatewayEndpointForLog(endpoint), poolSize, maxPhysical, maxStreams, tlsVerification,
	))
	forwarder, err := websocketmux.Start(ctx, websocketmux.ClientConfig{
		URL:               endpoint,
		Token:             config.Token,
		TLSConfig:         &tls.Config{InsecureSkipVerify: config.InsecureSkipVerify}, // #nosec G402 -- explicit user option
		PoolSize:          config.PoolSize,
		MaxPhysical:       config.MaxPhysical,
		MaxStreamsPerConn: config.MaxStreams,
	})
	if err != nil {
		if localForwarder != nil {
			_ = localForwarder.Close()
		}
		return nil, "Gateway WebSocket multiplexer", err
	}
	m.AppendLog("INFO", fmt.Sprintf(
		"Gateway WebSocket multiplexer ready: upstream=%s local=%s",
		gatewayEndpointForLog(endpoint), forwarder.Address(),
	))
	if localForwarder == nil {
		return forwarder, "Gateway WebSocket multiplexer", nil
	}
	return &combinedGatewayForwarder{
		primary: forwarder,
		local:   localForwarder,
	}, "Gateway WebSocket multiplexer (local test)", nil
}

// localWebSocketEndpoint turns the UI's localhost:8080 development URL into
// an ephemeral API-server port-forward. The in-cluster HTTP listener is
// intentionally cleartext; TLS belongs at the production Ingress boundary.
func (m *Manager) localWebSocketEndpoint(
	ctx context.Context, contextName, podName, endpoint string,
) (string, cluster.PortForward, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || !isLocalGatewayHTTPURL(parsed) {
		return endpoint, nil, nil
	}
	forwarder, err := m.gateway.StartPortForward(
		ctx, contextName, podName, cluster.GatewayHTTPPort,
	)
	if err != nil {
		return "", nil, fmt.Errorf("start local HTTP Gateway port-forward: %w", err)
	}
	parsed.Host = forwarder.Address()
	parsed.Scheme = "ws"
	return parsed.String(), forwarder, nil
}

func gatewayEndpointForLog(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "<invalid>"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func effectiveMultiplexingLimits(config store.GatewayTransport) (int, int, int) {
	poolSize := config.PoolSize
	if poolSize <= 0 {
		poolSize = 2
	}
	maxPhysical := config.MaxPhysical
	if maxPhysical <= 0 {
		maxPhysical = 4
	}
	if maxPhysical < poolSize {
		maxPhysical = poolSize
	}
	maxStreams := config.MaxStreams
	if maxStreams <= 0 {
		maxStreams = 128
	}
	return poolSize, maxPhysical, maxStreams
}

func isLocalGatewayHTTPURL(endpoint *url.URL) bool {
	if endpoint == nil || endpoint.Port() != strconv.Itoa(cluster.GatewayHTTPPort) {
		return false
	}
	host := endpoint.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

type combinedGatewayForwarder struct {
	primary cluster.PortForward
	local   cluster.PortForward
}

func (f *combinedGatewayForwarder) Address() string { return f.primary.Address() }

func (f *combinedGatewayForwarder) Close() error {
	return errors.Join(f.primary.Close(), f.local.Close())
}
