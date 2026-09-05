package dataplane

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	clienttraffic "github.com/fengqi-dev/kube-loop/internal/client/traffic"
	clientwebsocketmux "github.com/fengqi-dev/kube-loop/internal/client/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/gateway"
	"github.com/fengqi-dev/kube-loop/internal/gateway/trojanproxy"
	"github.com/fengqi-dev/kube-loop/internal/gateway/trojanruntime"
	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trojanws"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

func TestE2ESharedTunnelPathCarriesControlTCPAndUDP(t *testing.T) {
	binary := os.Getenv("KUBELOOP_SINGBOX_PATH")
	if binary == "" {
		t.Skip("KUBELOOP_SINGBOX_PATH is not set")
	}
	targetIP := privateHostIP(t)
	target := listenTCPEcho(t)
	udpTarget := listenUDPEcho(t)
	_, rawTargetPort, err := net.SplitHostPort(target.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	targetAddress := net.JoinHostPort(targetIP, rawTargetPort)

	const sessionID = "33333333-3333-4333-8333-333333333333"
	network, err := networkspec.Normalize(networkspec.Spec{PodIPs: []string{targetIP}})
	if err != nil {
		t.Fatal(err)
	}
	networkHash, err := networkspec.Hash(network)
	if err != nil {
		t.Fatal(err)
	}
	tunnelURL := startSharedTunnelGateway(t, binary, sessionID, networkHash, network)

	clientPort, err := utils.FreeTCPPort()
	if err != nil {
		t.Fatal(err)
	}
	password, err := trojanws.DeriveSessionPassword(sessionID, 1)
	if err != nil {
		t.Fatal(err)
	}
	clientConfig, err := singbox.GenerateClientTrojanConfig(singbox.ClientTrojanOptions{
		SessionID: sessionID, ListenPort: clientPort,
		Endpoint:    tunnelURL,
		RelayTicket: "e2e-ticket", TrojanPassword: password,
	})
	if err != nil {
		t.Fatal(err)
	}
	startSingBox(t, binary, "client", clientConfig)

	dialer := clienttraffic.Dialer{Endpoint: clienttraffic.Endpoint{
		Address: net.JoinHostPort("127.0.0.1", strconv.Itoa(clientPort)),
	}}
	var connection net.Conn
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		connection, err = dialer.DialContext(context.Background(), "tcp", targetAddress)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("connect through Trojan/WebSocket: %v", err)
	}
	denyCtx, cancelDeny := context.WithTimeout(context.Background(), 2*time.Second)
	denied, denyErr := dialer.DialContext(denyCtx, "tcp", "1.1.1.1:80")
	cancelDeny()
	if denyErr == nil && denied != nil {
		_ = denied.SetDeadline(time.Now().Add(2 * time.Second))
		_, writeErr := denied.Write([]byte("GET / HTTP/1.0\r\nHost: one.one.one.one\r\n\r\n"))
		var response [1]byte
		_, readErr := denied.Read(response[:])
		_ = denied.Close()
		if writeErr == nil && readErr == nil {
			t.Fatal("public target bypassed the Gateway NetworkSpec")
		}
	} else if denyErr == nil {
		t.Fatal("public target was not rejected")
	}
	t.Cleanup(func() { _ = connection.Close() })
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := connection.Write([]byte("redis-like-ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("redis-like-ping"))
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "redis-like-ping" {
		t.Fatalf("response = %q", response)
	}
	_, rawUDPPort, err := net.SplitHostPort(udpTarget.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	udpConnection, err := dialer.DialContext(
		context.Background(), "udp", net.JoinHostPort(targetIP, rawUDPPort),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = udpConnection.Close() })
	_ = udpConnection.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := udpConnection.Write([]byte("dns-like-ping")); err != nil {
		t.Fatal(err)
	}
	udpResponse := make([]byte, len("dns-like-ping"))
	if _, err := io.ReadFull(udpConnection, udpResponse); err != nil {
		t.Fatal(err)
	}
	if string(udpResponse) != "dns-like-ping" {
		t.Fatalf("UDP response = %q", udpResponse)
	}
}

func startSharedTunnelGateway(
	t *testing.T,
	binary, sessionID, networkHash string,
	network networkspec.Spec,
) string {
	t.Helper()
	const tunnelPath = "/tunnel"
	if websocketmux.DefaultPath != tunnelPath || trojanproxy.DefaultPath != tunnelPath {
		t.Fatalf(
			"public tunnel paths = control %q, forward %q, want %q",
			websocketmux.DefaultPath, trojanproxy.DefaultPath, tunnelPath,
		)
	}
	runtimeCtx, runtimeCancel := context.WithCancel(context.Background())
	t.Cleanup(runtimeCancel)
	gatewayRuntime, err := trojanruntime.NewManager(runtimeCtx, trojanruntime.Config{BinaryPath: binary})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gatewayRuntime.Close() })
	identity := websocketmux.Identity{
		IdentityID: "user-1", DeviceID: "device-1", SessionID: sessionID,
		SessionGeneration: 1, Namespace: "default", NetworkSpecHash: networkHash,
		ExpiresAt: time.Now().Add(time.Minute),
	}
	authenticator := websocketmux.AuthenticatorFunc(
		func(request *http.Request) (websocketmux.Identity, error) {
			if request.Header.Get("Authorization") != "Bearer e2e-ticket" {
				return websocketmux.Identity{}, fmt.Errorf("invalid RelayTicket")
			}
			return identity, nil
		},
	)
	gatewayServer := gateway.NewServer(nil)
	gatewayServer.Forward = gatewayRuntime
	encryption := false
	controlHandler, err := websocketmux.NewHandler(websocketmux.ServerConfig{
		Authenticator: authenticator, TrafficEncryption: &encryption,
		Handle: func(ctx context.Context, authorized websocketmux.Identity, connection net.Conn) {
			gatewayServer.ServeConnForAuthorizationContext(ctx, connection, gateway.SessionAuthorization{
				IdentityID: authorized.IdentityID, DeviceID: authorized.DeviceID,
				SessionID: authorized.SessionID, Generation: authorized.SessionGeneration,
				Namespace: authorized.Namespace, NetworkSpecHash: authorized.NetworkSpecHash,
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := trojanproxy.NewHandler(trojanproxy.Config{
		Path: trojanproxy.DefaultPath, Authenticator: authenticator, Resolver: gatewayRuntime,
	})
	if err != nil {
		t.Fatal(err)
	}
	tunnelServer := httptest.NewServer(gateway.NewTunnelHandler(controlHandler, proxy))
	t.Cleanup(tunnelServer.Close)
	tunnelURL := "ws" + strings.TrimPrefix(tunnelServer.URL, "http") + trojanproxy.DefaultPath
	controlForwarder, err := clientwebsocketmux.Start(runtimeCtx, clientwebsocketmux.ClientConfig{
		URL: tunnelURL, Token: "e2e-ticket", ClientVersion: "test", DeviceID: identity.DeviceID,
		SessionID: sessionID, SessionGeneration: 1, TrafficEncryption: &encryption,
		PoolSize: 1, MaxPhysical: 1, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controlForwarder.Close() })
	control, err := (&net.Dialer{}).DialContext(runtimeCtx, "tcp", controlForwarder.Address())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Close() })
	token, err := tunnel.RelaySessionToken(sessionID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := tunnel.WriteAuthorizedControlSession(control, token, network); err != nil {
		t.Fatal(err)
	}
	if err := tunnel.ReadStatus(control); err != nil {
		t.Fatalf("register control session through shared /tunnel endpoint: %v", err)
	}
	return tunnelURL
}

func startSingBox(t *testing.T, binary, name string, config []byte) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), name+".json")
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var output bytes.Buffer
	command := exec.CommandContext(ctx, binary, "run", "-c", configPath)
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if err := command.Wait(); err != nil && !strings.Contains(err.Error(), "signal: killed") {
			t.Logf("%s sing-box stopped: %v\n%s", name, err, output.String())
		}
	})
}

func listenTCPEcho(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer connection.Close()
				_, _ = io.Copy(connection, connection)
			}()
		}
	}()
	return listener
}

func listenUDPEcho(t *testing.T) *net.UDPConn {
	t.Helper()
	connection, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	go func() {
		buffer := make([]byte, 64<<10)
		for {
			n, source, readErr := connection.ReadFromUDP(buffer)
			if readErr != nil {
				return
			}
			_, _ = connection.WriteToUDP(buffer[:n], source)
		}
	}()
	return connection
}

func privateHostIP(t *testing.T) string {
	t.Helper()
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range addresses {
		prefix, parseErr := netip.ParsePrefix(raw.String())
		if parseErr == nil && prefix.Addr().Is4() && prefix.Addr().IsPrivate() && !prefix.Addr().IsLoopback() {
			return prefix.Addr().String()
		}
	}
	t.Skip("no private non-loopback IPv4 address available for Gateway route test")
	return ""
}
