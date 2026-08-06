//go:build e2e

package connect

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/e2e/harness"
	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/session"
)

func TestMain(m *testing.M) { harness.RunMain(m) }

func TestTUNConnectClusterIP(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := harness.TestContext(t, 1*time.Minute)
	defer cancel()

	provider := harness.NewProvider(t)
	client := harness.KubeClient(t, provider)
	if err := harness.EnsureEchoWorkload(ctx, client); err != nil {
		t.Fatal(err)
	}
	clusterIP := harness.EchoServiceIP(t, ctx, client)

	live := harness.ConnectSession(t, ctx, session.Request{
		Context: harness.KubeContext(), Namespace: harness.EchoNamespace,
	}, nil)
	if live.State.Network == nil {
		t.Fatal("connected session is missing network diagnostics")
	}
	if live.State.Network.RoutingMode != "native" {
		t.Fatalf("routing mode = %q, want native", live.State.Network.RoutingMode)
	}
	wantStrictRoute := runtime.GOOS != "windows"
	if live.State.Network.StrictRoute != wantStrictRoute {
		t.Fatalf(
			"strict route = %v, want %v on %s",
			live.State.Network.StrictRoute, wantStrictRoute, runtime.GOOS,
		)
	}

	harness.WaitHostTCP(t, clusterIP, 8080, "ping", "cluster-tcp:")
	harness.WaitHostUDP(t, clusterIP, 9090, "ping", "cluster-udp:")
}

func TestSOCKS5ConnectWithoutTUN(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := harness.TestContext(t, 1*time.Minute)
	defer cancel()

	provider := harness.NewProvider(t)
	client := harness.KubeClient(t, provider)
	if err := harness.EnsureEchoWorkload(ctx, client); err != nil {
		t.Fatal(err)
	}
	clusterIP := harness.EchoServiceIP(t, ctx, client)

	live := harness.ConnectSession(t, ctx, session.Request{
		Context: harness.KubeContext(), Namespace: harness.EchoNamespace,
		Mode: session.ConnectionModeSOCKS,
	}, nil)
	if live.State.Mode != session.ConnectionModeSOCKS {
		t.Fatalf("mode = %q, want %q", live.State.Mode, session.ConnectionModeSOCKS)
	}
	if live.State.SOCKSPort < 1 {
		t.Fatalf("SOCKS5 port = %d, want an allocated loopback port", live.State.SOCKSPort)
	}
	if live.State.Network == nil || live.State.Network.RoutingMode != "proxy" {
		t.Fatalf("SOCKS5 network diagnostics = %#v, want proxy routing", live.State.Network)
	}
	if live.State.Network.StrictRoute {
		t.Fatal("SOCKS5 mode unexpectedly enabled strict TUN routing")
	}
	if _, err := live.Manager.SingBoxConfig(); err == nil {
		t.Fatal("SOCKS5 mode unexpectedly started a sing-box TUN core")
	}
	if _, err := live.Manager.InternalDNSPort(); err == nil {
		t.Fatal("SOCKS5 mode unexpectedly started the TUN DNS listener")
	}
	assertNoPrivilegedSession(t)

	proxyAddress := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", live.State.SOCKSPort))
	waitSOCKSEcho(t, proxyAddress, clusterIP, 8080, "ip", "cluster-tcp:")
	waitSOCKSEcho(
		t,
		proxyAddress,
		"echo."+harness.EchoNamespace+".svc.cluster.local",
		8080,
		"dns",
		"cluster-tcp:",
	)
	waitSOCKSUDPEcho(t, proxyAddress, clusterIP, 9090, "udp-ip", "cluster-udp:")
	waitSOCKSUDPEcho(
		t,
		proxyAddress,
		"echo."+harness.EchoNamespace+".svc.cluster.local",
		9090,
		"udp-dns",
		"cluster-udp:",
	)
}

func TestTUNConnectPodIP(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := harness.TestContext(t, 1*time.Minute)
	defer cancel()

	provider := harness.NewProvider(t)
	client := harness.KubeClient(t, provider)
	if err := harness.EnsureEchoWorkload(ctx, client); err != nil {
		t.Fatal(err)
	}
	clusterIP := harness.EchoServiceIP(t, ctx, client)
	_, podIP := harness.EchoPodIP(t, ctx, client)

	_ = harness.ConnectSession(t, ctx, session.Request{
		Context: harness.KubeContext(), Namespace: harness.EchoNamespace,
	}, nil)

	harness.WaitHostTCP(t, clusterIP, 8080, "ping", "cluster-tcp:")
	harness.RequireRoutedViaKubeLoop(t, podIP, clusterIP)
	harness.WaitHostTCP(t, podIP, 8080, "ping", "cluster-tcp:")
	harness.WaitHostUDP(t, podIP, 9090, "ping", "cluster-udp:")
}

func assertNoPrivilegedSession(t *testing.T) {
	t.Helper()
	client, err := helper.NewClient()
	if err != nil {
		// An uninstalled helper is a valid SOCKS-only environment.
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	status, err := client.Ping(ctx)
	if err != nil {
		// A helper that is not running cannot own a TUN session.
		return
	}
	if status.PID != 0 || len(status.ActiveSessions) != 0 {
		t.Fatalf(
			"SOCKS5 mode started a privileged session: pid=%d sessions=%v",
			status.PID,
			status.ActiveSessions,
		)
	}
}

func waitSOCKSEcho(
	t *testing.T,
	proxyAddress, targetHost string,
	targetPort int,
	payload, wantPrefix string,
) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	var last string
	var lastErr error
	for time.Now().Before(deadline) {
		last, lastErr = dialSOCKSEcho(proxyAddress, targetHost, targetPort, payload)
		if lastErr == nil && strings.HasPrefix(last, wantPrefix) {
			return
		}
		time.Sleep(400 * time.Millisecond)
	}
	t.Fatalf(
		"SOCKS5 %s -> %s:%d: %v (last=%q)",
		proxyAddress,
		targetHost,
		targetPort,
		lastErr,
		last,
	)
}

func dialSOCKSEcho(proxyAddress, targetHost string, targetPort int, payload string) (string, error) {
	conn, err := net.DialTimeout("tcp", proxyAddress, 3*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return "", err
	}
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		return "", err
	}
	var greeting [2]byte
	if _, err := io.ReadFull(conn, greeting[:]); err != nil {
		return "", err
	}
	if greeting != [2]byte{5, 0} {
		return "", fmt.Errorf("SOCKS5 greeting response = %v", greeting)
	}

	request, err := socksConnectRequest(targetHost, targetPort)
	if err != nil {
		return "", err
	}
	if _, err := conn.Write(request); err != nil {
		return "", err
	}
	if err := readSOCKSReply(conn); err != nil {
		return "", err
	}
	if _, err := io.WriteString(conn, payload); err != nil {
		return "", err
	}
	response := make([]byte, 128)
	n, err := conn.Read(response)
	if err != nil {
		return "", err
	}
	return string(response[:n]), nil
}

func waitSOCKSUDPEcho(
	t *testing.T,
	proxyAddress, targetHost string,
	targetPort int,
	payload, wantPrefix string,
) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	var last string
	var lastErr error
	for time.Now().Before(deadline) {
		last, lastErr = dialSOCKSUDPEcho(proxyAddress, targetHost, targetPort, payload)
		if lastErr == nil && strings.HasPrefix(last, wantPrefix) {
			return
		}
		time.Sleep(400 * time.Millisecond)
	}
	t.Fatalf(
		"SOCKS5 UDP %s -> %s:%d: %v (last=%q)",
		proxyAddress,
		targetHost,
		targetPort,
		lastErr,
		last,
	)
}

func dialSOCKSUDPEcho(
	proxyAddress, targetHost string,
	targetPort int,
	payload string,
) (string, error) {
	control, err := net.DialTimeout("tcp", proxyAddress, 3*time.Second)
	if err != nil {
		return "", err
	}
	defer control.Close()
	if err := control.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return "", err
	}
	if _, err := control.Write([]byte{5, 1, 0}); err != nil {
		return "", err
	}
	var greeting [2]byte
	if _, err := io.ReadFull(control, greeting[:]); err != nil {
		return "", err
	}
	if greeting != [2]byte{5, 0} {
		return "", fmt.Errorf("SOCKS5 greeting response = %v", greeting)
	}
	if _, err := control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return "", err
	}
	relayHost, relayPort, err := readSOCKSReplyAddress(control)
	if err != nil {
		return "", err
	}
	if relayHost == "" || relayHost == "0.0.0.0" || relayHost == "::" {
		relayHost, _, err = net.SplitHostPort(proxyAddress)
		if err != nil {
			return "", err
		}
	}
	relay, err := net.ResolveUDPAddr("udp", net.JoinHostPort(relayHost, strconv.Itoa(relayPort)))
	if err != nil {
		return "", err
	}
	client, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		return "", err
	}
	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return "", err
	}

	request, err := socksConnectRequest(targetHost, targetPort)
	if err != nil {
		return "", err
	}
	packet := append([]byte{0, 0, 0}, request[3:]...)
	packet = append(packet, payload...)
	if _, err := client.Write(packet); err != nil {
		return "", err
	}
	response := make([]byte, 64<<10)
	n, err := client.Read(response)
	if err != nil {
		return "", err
	}
	if n < 4 || response[0] != 0 || response[1] != 0 || response[2] != 0 {
		return "", fmt.Errorf("invalid SOCKS5 UDP response")
	}
	reader := bytes.NewReader(response[3:n])
	if _, _, err := readSOCKSAddress(reader); err != nil {
		return "", err
	}
	return string(response[n-reader.Len() : n]), nil
}

func socksConnectRequest(host string, port int) ([]byte, error) {
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid target port %d", port)
	}
	request := []byte{5, 1, 0}
	if ip := net.ParseIP(host); ip != nil {
		if ipv4 := ip.To4(); ipv4 != nil {
			request = append(request, 1)
			request = append(request, ipv4...)
		} else {
			request = append(request, 4)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			return nil, fmt.Errorf("invalid SOCKS5 target host %q", host)
		}
		request = append(request, 3, byte(len(host)))
		request = append(request, host...)
	}
	var portBytes [2]byte
	binary.BigEndian.PutUint16(portBytes[:], uint16(port))
	return append(request, portBytes[:]...), nil
}

func readSOCKSReply(reader io.Reader) error {
	_, _, err := readSOCKSReplyAddress(reader)
	return err
}

func readSOCKSReplyAddress(reader io.Reader) (string, int, error) {
	var header [3]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return "", 0, err
	}
	if header[0] != 5 {
		return "", 0, fmt.Errorf("SOCKS reply version = %d", header[0])
	}
	if header[1] != 0 {
		return "", 0, fmt.Errorf("SOCKS connect failed with code %d", header[1])
	}
	return readSOCKSAddress(reader)
}

func readSOCKSAddress(reader io.Reader) (string, int, error) {
	var addressType [1]byte
	if _, err := io.ReadFull(reader, addressType[:]); err != nil {
		return "", 0, err
	}
	var addressLength int
	switch addressType[0] {
	case 1:
		addressLength = net.IPv4len
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(reader, length[:]); err != nil {
			return "", 0, err
		}
		addressLength = int(length[0])
	case 4:
		addressLength = net.IPv6len
	default:
		return "", 0, fmt.Errorf("SOCKS address type = %d", addressType[0])
	}
	address := make([]byte, addressLength)
	if _, err := io.ReadFull(reader, address); err != nil {
		return "", 0, err
	}
	var rawPort [2]byte
	if _, err := io.ReadFull(reader, rawPort[:]); err != nil {
		return "", 0, err
	}
	host := string(address)
	if addressType[0] != 3 {
		host = net.IP(address).String()
	}
	return host, int(binary.BigEndian.Uint16(rawPort[:])), nil
}

func TestTUNConnectManualNetwork(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := harness.TestContext(t, 1*time.Minute)
	defer cancel()

	provider := harness.NewProvider(t)
	client := harness.KubeClient(t, provider)
	if err := harness.EnsureEchoWorkload(ctx, client); err != nil {
		t.Fatal(err)
	}
	clusterIP := harness.EchoServiceIP(t, ctx, client)

	discovery, err := provider.Discover(ctx, harness.KubeContext(), []string{harness.EchoNamespace})
	if err != nil {
		t.Fatal(err)
	}
	if discovery.DNSServer == "" || len(discovery.ServiceCIDRs) == 0 {
		t.Fatalf("discovery incomplete: %#v", discovery)
	}

	live := harness.ConnectSession(t, ctx, session.Request{
		Context: harness.KubeContext(), Namespace: harness.EchoNamespace,
	}, func(manager *session.Manager) {
		if err := manager.SetManualNetwork(harness.KubeContext(), cluster.ManualNetwork{
			PodCIDRs:       discovery.PodCIDRs,
			ServiceCIDRs:   discovery.ServiceCIDRs,
			DNSServer:      discovery.DNSServer,
			ClusterDomains: discovery.ClusterDomains,
			DNSNamespace:   harness.EchoNamespace,
		}); err != nil {
			t.Fatal(err)
		}
	})

	harness.WaitHostTCP(t, clusterIP, 8080, "manual", "cluster-tcp:")
	dnsPort, err := live.Manager.InternalDNSPort()
	if err != nil {
		t.Fatal(err)
	}
	harness.WaitDNSA(
		t,
		dnsPort,
		"echo."+harness.EchoNamespace+".svc.cluster.local",
		clusterIP,
	)
}

func TestTUNDisconnectTearsDown(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := harness.TestContext(t, 1*time.Minute)
	defer cancel()

	provider := harness.NewProvider(t)
	client := harness.KubeClient(t, provider)
	if err := harness.EnsureEchoWorkload(ctx, client); err != nil {
		t.Fatal(err)
	}
	clusterIP := harness.EchoServiceIP(t, ctx, client)

	live := harness.ConnectSession(t, ctx, session.Request{
		Context: harness.KubeContext(), Namespace: harness.EchoNamespace,
	}, nil)
	fqdn := "echo." + harness.EchoNamespace + ".svc.cluster.local"
	harness.WaitHostTCP(t, clusterIP, 8080, "ping", "cluster-tcp:")
	harness.WaitLookupIP(t, fqdn, clusterIP)

	if err := live.Manager.Disconnect(); err != nil {
		t.Fatal(err)
	}
	harness.AssertHelperIdle(t)
	harness.AssertClusterDNSGone(t, fqdn, clusterIP)
}
