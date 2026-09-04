package dataplane

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/socksbridge"
	"github.com/fengqi-dev/kube-loop/internal/protocol/sessionspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

type testTickets struct {
	mu      sync.Mutex
	calls   int
	session remote.Session
	refresh func(remote.Session) (remote.Session, error)
	updates chan remote.SessionUpdate
}

type testTUNStarter struct {
	mu            sync.Mutex
	starts        int
	network       singbox.NetworkSpec
	bridgeAddress string
	namespace     string
	hosts         []sessionspec.HostAlias
	core          *testCore
}

type blockingTUNStarter struct {
	*testTUNStarter

	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
}

func (starter *blockingTUNStarter) Start(
	ctx context.Context,
	network singbox.NetworkSpec,
	bridgeAddress, namespace string,
	hosts []sessionspec.HostAlias,
) (singbox.RunningCore, error) {
	starter.startedOnce.Do(func() { close(starter.started) })
	select {
	case <-starter.release:
		return starter.testTUNStarter.Start(ctx, network, bridgeAddress, namespace, hosts)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (starter *blockingTUNStarter) unblock() {
	starter.releaseOnce.Do(func() { close(starter.release) })
}

func (starter *testTUNStarter) Start(
	ctx context.Context,
	network singbox.NetworkSpec,
	bridgeAddress, namespace string,
	hosts []sessionspec.HostAlias,
) (singbox.RunningCore, error) {
	starter.mu.Lock()
	defer starter.mu.Unlock()
	starter.starts++
	starter.network = network
	starter.bridgeAddress = bridgeAddress
	starter.namespace = namespace
	starter.hosts = append([]sessionspec.HostAlias{}, hosts...)
	starter.core = &testCore{done: make(chan struct{}), sessionID: "tun-session"}
	go func(core *testCore) {
		select {
		case <-ctx.Done():
			_ = core.Close()
		case <-core.Done():
		}
	}(starter.core)
	return starter.core, nil
}

func (starter *testTUNStarter) snapshot() (int, singbox.NetworkSpec, string, string, *testCore) {
	starter.mu.Lock()
	defer starter.mu.Unlock()
	return starter.starts, starter.network, starter.bridgeAddress, starter.namespace, starter.core
}

type testCore struct {
	mu           sync.Mutex
	done         chan struct{}
	closeOnce    sync.Once
	sessionID    string
	dnsNamespace string
	hosts        []sessionspec.HostAlias
	dnsErr       error
	hostsErr     error
	logsErr      error
	closeErr     error
	closeCalls   int
}

func (core *testCore) Close() error {
	core.mu.Lock()
	core.closeCalls++
	core.mu.Unlock()
	core.closeOnce.Do(func() { close(core.done) })
	return core.closeErr
}
func (core *testCore) Done() <-chan struct{} { return core.done }
func (core *testCore) Err() error            { return nil }
func (core *testCore) SessionID() string     { return core.sessionID }
func (core *testCore) Config() []byte        { return []byte(`{"version":2}`) }
func (core *testCore) ReadLogs(context.Context) ([]string, error) {
	return []string{"ready"}, core.logsErr
}
func (core *testCore) UpdateDNSNamespace(_ context.Context, namespace string) error {
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.dnsErr != nil {
		return core.dnsErr
	}
	core.dnsNamespace = namespace
	return nil
}
func (core *testCore) UpdateHostAliases(_ context.Context, hosts []sessionspec.HostAlias) error {
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.hostsErr != nil {
		return core.hostsErr
	}
	core.hosts = append([]sessionspec.HostAlias{}, hosts...)
	return nil
}
func (core *testCore) ProbeClusterDNS(context.Context) error { return nil }
func (core *testCore) DNSPort() int                          { return 1053 }
func (core *testCore) InternalDNSPort() int                  { return 1054 }

func (tickets *testTickets) RelayTicketSource(string) func(context.Context) (remote.RelayTicket, error) {
	return func(context.Context) (remote.RelayTicket, error) {
		tickets.mu.Lock()
		defer tickets.mu.Unlock()
		tickets.calls++
		return remote.RelayTicket{Ticket: "relay-ticket"}, nil
	}
}

func (tickets *testTickets) Current(string) (remote.Session, error) {
	tickets.mu.Lock()
	defer tickets.mu.Unlock()
	return tickets.session, nil
}

func (tickets *testTickets) SessionUpdates() <-chan remote.SessionUpdate {
	return tickets.updates
}

func (tickets *testTickets) Refresh(context.Context, string) (remote.Session, error) {
	tickets.mu.Lock()
	defer tickets.mu.Unlock()
	if tickets.refresh != nil {
		next, err := tickets.refresh(tickets.session)
		if err == nil {
			tickets.session = next
		}
		return next, err
	}
	return tickets.session, nil
}

func acceptTestControl(listener net.Listener) {
	connection, err := listener.Accept()
	if err != nil {
		return
	}
	defer func() {
		_ = connection.Close() // The background accept helper closes only to release the test connection.
	}()
	header, err := tunnel.ReadSessionHeader(connection)
	if err != nil || header.Command != tunnel.CommandControl {
		return
	}
	if _, err := tunnel.ReadAuthorizedControlSpec(connection); err != nil {
		return
	}
	if err := tunnel.WriteStatus(connection, nil); err != nil {
		return
	}
	var buffer [1]byte
	_, _ = connection.Read(buffer[:])
}

func acceptTestControlWithSignal(listener net.Listener, controls chan<- net.Conn) {
	connection, err := listener.Accept()
	if err != nil {
		return
	}
	header, err := tunnel.ReadSessionHeader(connection)
	if err != nil || header.Command != tunnel.CommandControl {
		_ = connection.Close()
		return
	}
	if _, err := tunnel.ReadAuthorizedControlSpec(connection); err != nil {
		_ = connection.Close()
		return
	}
	if err := tunnel.WriteStatus(connection, nil); err != nil {
		_ = connection.Close()
		return
	}
	controls <- connection
}

func receiveControl(t *testing.T, controls <-chan net.Conn) net.Conn {
	t.Helper()
	select {
	case connection := <-controls:
		return connection
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Data Plane control stream")
		return nil
	}
}

type testForwarder struct {
	net.Listener

	open       func(context.Context) (net.Conn, error)
	closeErr   error
	closeCalls atomic.Int32
}

func (forwarder *testForwarder) Address() string { return forwarder.Listener.Addr().String() }
func (forwarder *testForwarder) OpenStream(ctx context.Context) (net.Conn, error) {
	if forwarder.open != nil {
		return forwarder.open(ctx)
	}
	return (&net.Dialer{}).DialContext(ctx, "tcp", forwarder.Address())
}
func (forwarder *testForwarder) Close() error {
	forwarder.closeCalls.Add(1)
	_ = forwarder.Listener.Close()
	return forwarder.closeErr
}

type testBridge struct {
	mu         sync.Mutex
	address    net.Addr
	closed     bool
	closeErr   error
	closeCalls int
	gateway    string
	token      tunnel.SessionToken
	hostTCP    socksbridge.HostTCPHandler
	logHandler socksbridge.LogHandler
}

func (bridge *testBridge) Addr() net.Addr { return bridge.address }
func (bridge *testBridge) Close() error {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.closed = true
	bridge.closeCalls++
	return bridge.closeErr
}
func (bridge *testBridge) SetGatewayAddress(address string) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.gateway = address
}
func (bridge *testBridge) SetGateway(address string, token tunnel.SessionToken) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.gateway = address
	bridge.token = token
}
func (bridge *testBridge) SetHostTCPHandler(handler socksbridge.HostTCPHandler) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.hostTCP = handler
}
func (bridge *testBridge) SetLogHandler(handler socksbridge.LogHandler) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.logHandler = handler
}
func (bridge *testBridge) snapshot() (string, bool) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return bridge.gateway, bridge.closed
}
func (bridge *testBridge) transport() (string, tunnel.SessionToken) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return bridge.gateway, bridge.token
}

type testAddress string

func (address testAddress) Network() string { return "tcp" }
func (address testAddress) String() string  { return string(address) }

type testCloseConn struct {
	net.Conn

	closeErr   error
	closeCalls int
}

func (connection *testCloseConn) Close() error {
	connection.closeCalls++
	_ = connection.Conn.Close()
	return connection.closeErr
}

type readSignalConn struct {
	net.Conn

	once sync.Once
	read chan struct{}
}

func (connection *readSignalConn) Read(buffer []byte) (int, error) {
	count, err := connection.Conn.Read(buffer)
	if count > 0 {
		connection.once.Do(func() { close(connection.read) })
	}
	return count, err
}
