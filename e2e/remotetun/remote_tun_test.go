//go:build e2e

package remotetun

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/e2e/harness"
	clientapp "github.com/fengqi-dev/kube-loop/internal/app"
	"github.com/fengqi-dev/kube-loop/internal/auth/relaybearer"
	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	"github.com/fengqi-dev/kube-loop/internal/client/powerwatch"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/gateway"
	gatewaymux "github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
)

const (
	remoteTargetIP = "100.64.0.42"
	remoteTarget   = "100.64.0.42:443"
	remoteIssuer   = "https://remote-tun.e2e.invalid"
	remoteRelayID  = "portable"
	remoteKeyID    = "e2e"
	remotePath     = "/tunnel"
)

type remoteSessionSource struct {
	mu       sync.Mutex
	signer   *relayticket.Signer
	endpoint string
	identity string
	device   string
	session  clientremote.Session
}

func (source *remoteSessionSource) RelayTicketSource(string) func(context.Context) (clientremote.RelayTicket, error) {
	return func(context.Context) (clientremote.RelayTicket, error) {
		source.mu.Lock()
		defer source.mu.Unlock()
		now := time.Now().UTC().Truncate(time.Second)
		ticket, err := source.signer.Sign(relayticket.Claims{
			Version: relayticket.Version, Issuer: remoteIssuer, Audience: remoteRelayID,
			IdentityID: source.identity, DeviceID: source.device,
			SessionID: source.session.ID, SessionGeneration: source.session.Generation,
			Namespace: source.session.Namespace, NetworkSpecHash: source.session.NetworkSpecHash,
			Operations: []string{"tunnel"}, TicketID: uuid.NewString(),
			IssuedAt: now.Unix(), NotBefore: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(),
			TrafficEncryption: new(false),
		})
		return clientremote.RelayTicket{
			TokenType: relayticket.Type, Ticket: ticket, ExpiresAt: now.Add(time.Minute),
			RelayID: remoteRelayID, Endpoint: source.endpoint, DeviceID: source.device,
			TrafficEncryption: new(false),
		}, err
	}
}

func (source *remoteSessionSource) Current(string) (clientremote.Session, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.session, nil
}

func (source *remoteSessionSource) Refresh(context.Context, string) (clientremote.Session, error) {
	return source.Current("")
}

type mappedDialer struct{ target string }

func (dialer mappedDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" || address != remoteTarget {
		return nil, errors.New("portable Gateway fixture rejected an unexpected target")
	}
	return (&net.Dialer{}).DialContext(ctx, "tcp", dialer.target)
}

func TestRemoteGatewayTUNSurvivesSystemWakeRefresh(t *testing.T) {
	if os.Getenv("KUBELOOP_REMOTE_TUN_E2E") != "1" {
		t.Skip("set KUBELOOP_REMOTE_TUN_E2E=1 to run the real Helper/TUN/WSS test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	harness.StopAllHelperSessions()
	t.Cleanup(harness.StopAllHelperSessions)

	echo := startEcho(t)
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	server, signer := startGateway(t, echo, publicKey, privateKey)
	spec, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{remoteTargetIP}})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := networkspec.Hash(spec)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session := clientremote.Session{
		ID: uuid.NewString(), Namespace: "remote-e2e", State: "active", Generation: 1,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(10 * time.Minute),
		NetworkSpec: spec, NetworkSpecHash: hash,
	}
	source := &remoteSessionSource{
		signer: signer, endpoint: "ws" + strings.TrimPrefix(server.URL, "http") + remotePath,
		identity: uuid.NewString(), device: uuid.NewString(), session: session,
	}
	events := make(chan clientdataplane.StatusEvent, 16)
	manager, err := clientdataplane.NewManager(source, clientdataplane.Config{
		StartTimeout: 15 * time.Second, RecoveryAttempts: 5, RecoveryBackoff: 100 * time.Millisecond,
		TUNStarter: clientapp.NewSingboxRuntime(nil),
		OnStatus:   func(event clientdataplane.StatusEvent) { events <- event },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	serverProfile := clientprofile.Profile{ID: "portable-remote", BaseURL: server.URL, TunnelPath: remotePath}
	connected, err := manager.Connect(ctx, serverProfile, session)
	if err != nil {
		t.Fatal(err)
	}
	tunStatus, err := manager.StartTUN(ctx, serverProfile.ID)
	if err != nil || tunStatus.Mode != "tun" || tunStatus.SOCKSAddress != connected.SOCKSAddress {
		t.Fatalf("start portable remote TUN: status=%#v err=%v", tunStatus, err)
	}
	helperClient, err := helper.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	helperSession := onlyHelperSession(t, ctx, helperClient)
	assertTUNTCP(t, remoteTarget, "before-wake")

	wakeEvents := make(chan powerwatch.Event, 1)
	wakeWatcher, err := powerwatch.New(powerwatch.Config{
		Interval: 100 * time.Millisecond, WakeGap: 500 * time.Millisecond,
		OnWake: func(event powerwatch.Event) {
			wakeEvents <- event
			if resumed := manager.ResumeAll(); resumed != 1 {
				t.Errorf("wake refresh scheduled %d Profiles, want 1", resumed)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !wakeWatcher.Observe(time.Now().Add(time.Second)) {
		t.Fatal("portable suspend gap did not emit a wake event")
	}
	select {
	case event := <-wakeEvents:
		if event.SleptFor < 500*time.Millisecond {
			t.Fatalf("portable wake event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("portable wake callback was not invoked")
	}
	waitForWakeRecovery(t, ctx, manager, serverProfile.ID, connected.SOCKSAddress, events)
	if after := onlyHelperSession(t, ctx, helperClient); after != helperSession {
		t.Fatalf("wake refresh reinstalled Helper TUN: before=%q after=%q", helperSession, after)
	}
	assertTUNTCP(t, remoteTarget, "after-wake")

	if _, err := manager.StopTUN(serverProfile.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Disconnect(serverProfile.ID); err != nil {
		t.Fatal(err)
	}
	waitForNoHelperSessions(t, ctx, helperClient)
}

func startGateway(
	t *testing.T,
	echo net.Listener,
	publicKey ed25519.PublicKey,
	privateKey ed25519.PrivateKey,
) (*httptest.Server, *relayticket.Signer) {
	t.Helper()
	signer, err := relayticket.NewSigner(remoteKeyID, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := relayticket.NewVerifier(relayticket.VerifierConfig{
		Keys: map[string]ed25519.PublicKey{remoteKeyID: publicKey}, Issuer: remoteIssuer,
		Audience: remoteRelayID, RequiredOperation: "tunnel",
	})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := relaybearer.NewReplayGuard(128, nil)
	if err != nil {
		t.Fatal(err)
	}
	requestVerifier, err := relaybearer.NewRequestVerifier(verifier, replay)
	if err != nil {
		t.Fatal(err)
	}
	core := gateway.NewServer(slog.New(slog.NewTextHandler(io.Discard, nil)), 5*time.Second)
	core.Dialer = mappedDialer{target: echo.Addr().String()}
	handler, err := gatewaymux.NewHandler(gatewaymux.ServerConfig{
		Authenticator: gatewaymux.AuthenticatorFunc(func(request *http.Request) (gatewaymux.Identity, error) {
			claims, err := requestVerifier.Verify(request)
			if err != nil {
				return gatewaymux.Identity{}, err
			}
			return gatewaymux.Identity{
				IdentityID: claims.IdentityID, Groups: append([]string(nil), claims.Groups...),
				DeviceID: claims.DeviceID, SessionID: claims.SessionID,
				SessionGeneration: claims.SessionGeneration, Namespace: claims.Namespace,
				NetworkSpecHash: claims.NetworkSpecHash, ExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC(),
				TrafficEncryption: claims.TrafficEncryption,
			}, nil
		}),
		ServerVersion: "e2e", MaxSessions: 8, MaxSessionsPerUser: 2, MaxStreamsPerSession: 16,
		TrafficEncryption: new(false),
		Handle: func(ctx context.Context, identity gatewaymux.Identity, connection net.Conn) {
			core.ServeConnForAuthorizationContext(ctx, connection, gateway.SessionAuthorization{
				RequestID: identity.RequestID, IdentityID: identity.IdentityID,
				Groups: append([]string(nil), identity.Groups...), DeviceID: identity.DeviceID,
				SessionID: identity.SessionID, Generation: identity.SessionGeneration,
				Namespace: identity.Namespace, NetworkSpecHash: identity.NetworkSpecHash,
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(remotePath, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, signer
}

func startEcho(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func(connection net.Conn) {
				defer connection.Close()
				payload := make([]byte, 4096)
				size, err := connection.Read(payload)
				if err == nil && size > 0 {
					_, _ = connection.Write(append([]byte("remote:"), payload[:size]...))
				}
			}(connection)
		}
	}()
	return listener
}

func assertTUNTCP(t *testing.T, address, payload string) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		t.Fatalf("dial real TUN target: %v", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := connection.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	want := "remote:" + payload
	response := make([]byte, len(want))
	if _, err := io.ReadFull(connection, response); err != nil || string(response) != want {
		t.Fatalf("real remote Gateway TUN response=%q want=%q err=%v", response, want, err)
	}
}

func waitForWakeRecovery(
	t *testing.T,
	ctx context.Context,
	manager *clientdataplane.Manager,
	profileID, socksAddress string,
	events <-chan clientdataplane.StatusEvent,
) {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	sawWake := false
	for {
		select {
		case event := <-events:
			if event.Reason == "system_resumed" && event.Status.State == "reconnecting" {
				sawWake = true
			}
		default:
		}
		status, err := manager.Status(profileID)
		if sawWake && err == nil && status.State == "connected" && status.Mode == "tun" &&
			status.SOCKSAddress == socksAddress {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatalf("wake recovery did not converge: status=%#v err=%v sawWake=%t", status, err, sawWake)
		case <-ticker.C:
		}
	}
}

func onlyHelperSession(t *testing.T, ctx context.Context, client *helper.Client) string {
	t.Helper()
	response, err := client.Status(ctx)
	if err != nil || len(response.ActiveSessions) != 1 {
		t.Fatalf("Helper active Sessions=%v err=%v", response.ActiveSessions, err)
	}
	return response.ActiveSessions[0]
}

func waitForNoHelperSessions(t *testing.T, ctx context.Context, client *helper.Client) {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		response, err := client.Status(ctx)
		if err == nil && len(response.ActiveSessions) == 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatalf("Helper retained TUN Sessions: %v err=%v", response.ActiveSessions, err)
		case <-ticker.C:
		}
	}
}
