package intercept

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/fengqi-dev/kube-loop/internal/tunnel"
)

// HostTCP returns a serve callback when host:port is an active intercept /
// preview target. Used by the local SOCKS bridge for TUN traffic.
func (m *Manager) HostTCP(host string, port uint16) (func(net.Conn), bool) {
	m.mu.Lock()
	route, ok := m.routes.lookup(host, port)
	if !ok {
		m.mu.Unlock()
		return nil, false
	}
	ctx := m.ctx
	gatewayAddress := m.gatewayAddress
	sessionToken := m.sessionToken
	dialers := m.traffic
	m.mu.Unlock()

	return func(client net.Conn) {
		m.serveHostTCP(ctx, gatewayAddress, sessionToken, client, route, dialers)
	}, true
}

// HostUDP returns a dialer when host:port is an active UDP intercept / preview
// target. Used by the local SOCKS bridge so host TUN UDP does not hairpin
// through the Gateway ClusterIP.
func (m *Manager) HostUDP(host string, port uint16) (func(context.Context) (net.Conn, error), bool) {
	m.mu.Lock()
	route, ok := m.routes.lookup(host, port)
	if !ok {
		m.mu.Unlock()
		return nil, false
	}
	gatewayAddress := m.gatewayAddress
	sessionToken := m.sessionToken
	m.mu.Unlock()

	return func(ctx context.Context) (net.Conn, error) {
		return m.dialHostUDP(ctx, gatewayAddress, sessionToken, route)
	}, true
}

func (m *Manager) serveHostTCP(
	ctx context.Context,
	gatewayAddress string,
	sessionToken tunnel.SessionToken,
	client net.Conn,
	route hostRoute,
	dialers TrafficDialers,
) {
	defer client.Close()
	host := cmp.Or(route.local.LocalHost, "127.0.0.1")
	localTarget := net.JoinHostPort(host, fmt.Sprintf("%d", route.local.LocalPort))

	if route.mode == ModeMirror {
		m.serveMirrorTCP(
			ctx, gatewayAddress, sessionToken, client,
			route.primaryAddr, host, route.local.LocalPort,
			dialers.MirrorShadow,
		)
		return
	}

	localDialer := dialers.Exchange
	if route.preview {
		localDialer = dialers.Preview
	}
	localConn, err := dialTraffic(ctx, localDialer, "tcp", localTarget)
	if err != nil {
		return
	}
	defer localConn.Close()
	relayTCP(client, localConn)
}

func (m *Manager) dialHostUDP(
	ctx context.Context,
	gatewayAddress string,
	sessionToken tunnel.SessionToken,
	route hostRoute,
) (net.Conn, error) {
	host := cmp.Or(route.local.LocalHost, "127.0.0.1")
	localTarget := net.JoinHostPort(host, fmt.Sprintf("%d", route.local.LocalPort))

	if route.mode == ModeMirror {
		return m.dialHostMirrorUDP(
			ctx, gatewayAddress, sessionToken,
			route.primaryAddr, host, route.local.LocalPort,
		)
	}

	// Dial the local process directly. Re-entering sing-box via exchange/preview
	// SOCKS UDP ASSOCIATE from the kubernetes outbound path times out on Linux with
	// auto_redirect, while the same HostTCP CONNECT re-entry works.
	var dialer net.Dialer
	return dialer.DialContext(ctx, "udp", localTarget)
}

func (m *Manager) dialHostMirrorUDP(
	ctx context.Context,
	gatewayAddress string,
	sessionToken tunnel.SessionToken,
	primaryAddr, localHost string,
	localPort int,
) (net.Conn, error) {
	if primaryAddr == "" {
		return nil, fmt.Errorf("mirror primary address is required")
	}
	primary, primaryFramed, err := dialMirrorPrimary(
		ctx, gatewayAddress, sessionToken, primaryAddr, tunnel.NetworkUDP,
	)
	if err != nil {
		return nil, err
	}
	localAddr := net.JoinHostPort(localHost, fmt.Sprintf("%d", localPort))
	var dialer net.Dialer
	localConn, err := dialer.DialContext(ctx, "udp", localAddr)
	if err != nil {
		localConn = nil
	}
	return newHostMirrorUDPConn(primary, primaryFramed, localConn), nil
}

func (m *Manager) handleReady(interceptSubID string, network byte, streamID uint64) {
	m.mu.Lock()
	gatewayAddress := m.gatewayAddress
	sessionToken := m.sessionToken
	local, primaryAddr, mode, preview, found := m.registry.findPort(interceptSubID)
	ctx := m.ctx
	dialers := m.traffic
	m.mu.Unlock()
	if !found || gatewayAddress == "" {
		return
	}
	localDialer := dialers.Exchange
	if preview {
		localDialer = dialers.Preview
	}
	go m.serveInbound(
		ctx, gatewayAddress, sessionToken, streamID, network, local, mode, primaryAddr,
		localDialer, dialers.MirrorShadow,
	)
}

func (m *Manager) serveInbound(
	ctx context.Context,
	gatewayAddress string,
	sessionToken tunnel.SessionToken,
	streamID uint64,
	network byte,
	local PortMapping,
	mode string,
	primaryAddr string,
	localDialer TrafficDialer,
	mirrorShadowDialer TrafficDialer,
) {
	tunnelConn, err := acceptStream(ctx, gatewayAddress, sessionToken, streamID)
	if err != nil {
		return
	}
	host := cmp.Or(local.LocalHost, "127.0.0.1")
	if mode == ModeMirror {
		m.serveMirror(
			ctx, gatewayAddress, sessionToken, tunnelConn, network,
			primaryAddr, host, local.LocalPort, mirrorShadowDialer,
		)
		return
	}
	target := net.JoinHostPort(host, fmt.Sprintf("%d", local.LocalPort))
	switch network {
	case tunnel.NetworkUDP:
		// Gateway inbound UDP already arrived through the authenticated reverse
		// tunnel. Re-entering sing-box's feature SOCKS UDP inbound can loop or
		// time out; the local target is always a host listener, so dial directly.
		var direct net.Dialer
		localConn, err := direct.DialContext(ctx, "udp", target)
		if err != nil {
			_ = tunnelConn.Close()
			return
		}
		relayUDPConn(tunnelConn, localConn)
	default:
		localConn, err := dialTraffic(ctx, localDialer, "tcp", target)
		if err != nil {
			_ = tunnelConn.Close()
			return
		}
		defer localConn.Close()
		relayTCP(tunnelConn, localConn)
	}
}

func (m *Manager) serveMirror(
	ctx context.Context,
	gatewayAddress string,
	sessionToken tunnel.SessionToken,
	client net.Conn,
	network byte,
	primaryAddr, localHost string,
	localPort int,
	shadowDialer TrafficDialer,
) {
	if primaryAddr == "" {
		_ = client.Close()
		return
	}
	if network == tunnel.NetworkUDP {
		m.serveMirrorUDP(
			ctx, gatewayAddress, sessionToken, client, primaryAddr, localHost, localPort,
		)
		return
	}
	m.serveMirrorTCP(
		ctx, gatewayAddress, sessionToken,
		client, primaryAddr, localHost, localPort, shadowDialer,
	)
}

func (m *Manager) serveMirrorTCP(
	ctx context.Context,
	gatewayAddress string,
	sessionToken tunnel.SessionToken,
	client net.Conn,
	primaryAddr, localHost string,
	localPort int,
	shadowDialer TrafficDialer,
) {
	primary, _, err := dialMirrorPrimary(
		ctx, gatewayAddress, sessionToken, primaryAddr, tunnel.NetworkTCP,
	)
	if err != nil {
		_ = client.Close()
		return
	}
	localConn, err := dialTraffic(
		ctx, shadowDialer, "tcp", net.JoinHostPort(localHost, fmt.Sprintf("%d", localPort)),
	)
	if err != nil {
		localConn = nil
	}
	mirrorTCP(client, primary, localConn)
}

func (m *Manager) serveMirrorUDP(
	ctx context.Context,
	gatewayAddress string,
	sessionToken tunnel.SessionToken,
	client net.Conn,
	primaryAddr, localHost string,
	localPort int,
) {
	primary, primaryFramed, err := dialMirrorPrimary(
		ctx, gatewayAddress, sessionToken, primaryAddr, tunnel.NetworkUDP,
	)
	if err != nil {
		_ = client.Close()
		return
	}
	localAddr := net.JoinHostPort(localHost, fmt.Sprintf("%d", localPort))
	// As with exchange/preview UDP, the shadow target is a host listener and
	// must not re-enter sing-box's SOCKS UDP inbound.
	var direct net.Dialer
	localConn, err := direct.DialContext(ctx, "udp", localAddr)
	if err != nil {
		localConn = nil
	}
	mirrorUDP(client, primary, primaryFramed, localConn)
}

// dialMirrorPrimary reaches the original Pod backend via Gateway outbound dial
// so Pod IPs work without a host route/TUN. Loopback addresses used in unit
// tests fall back to a direct dial.
// framed is true when the returned conn uses tunnel datagram framing (Gateway UDP).
func dialMirrorPrimary(
	ctx context.Context,
	gatewayAddress string,
	sessionToken tunnel.SessionToken,
	primaryAddr string,
	network byte,
) (conn net.Conn, framed bool, err error) {
	host, portStr, err := net.SplitHostPort(primaryAddr)
	if err != nil {
		return nil, false, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return nil, false, fmt.Errorf("invalid primary port %q", portStr)
	}
	command := tunnel.CommandTCP
	if network == tunnel.NetworkUDP {
		command = tunnel.CommandUDP
	}
	if gatewayAddress != "" && !isLoopbackHost(host) {
		conn, err := dialGatewayOpen(
			ctx, gatewayAddress, sessionToken, command, host, uint16(port),
		)
		if err == nil {
			return conn, network == tunnel.NetworkUDP, nil
		}
	}
	if network == tunnel.NetworkUDP {
		udpAddr, err := net.ResolveUDPAddr("udp", primaryAddr)
		if err != nil {
			return nil, false, err
		}
		conn, err := net.DialUDP("udp", nil, udpAddr)
		return conn, false, err
	}
	var dialer net.Dialer
	conn, err = dialer.DialContext(ctx, "tcp", primaryAddr)
	return conn, false, err
}

func dialTraffic(
	ctx context.Context, dialer TrafficDialer, network, address string,
) (net.Conn, error) {
	if dialer != nil {
		return dialer.DialContext(ctx, network, address)
	}
	var direct net.Dialer
	return direct.DialContext(ctx, network, address)
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func dialGatewayOpen(
	ctx context.Context,
	gatewayAddress string,
	sessionToken tunnel.SessionToken,
	command byte,
	host string,
	port uint16,
) (net.Conn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", gatewayAddress)
	if err != nil {
		return nil, fmt.Errorf("connect gateway: %w", err)
	}
	if err := tunnel.WriteOpen(conn, tunnel.OpenRequest{
		Command: command, Host: host, Port: port,
	}, sessionToken); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := tunnel.ReadStatus(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}
