package dataplane

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/sessionspec"
)

func TestRuntimeNetworkSettingsCommitOnlyAfterCoreUpdate(t *testing.T) {
	dnsFailure := errors.New("dNS update failed")
	hostsFailure := errors.New("host alias update failed")
	core := &testCore{
		done: make(chan struct{}), dnsErr: dnsFailure, hostsErr: hostsFailure,
	}
	runtime := &Runtime{
		session: remote.Session{Namespace: "payments"}, tun: core,
		dnsNamespace: "development",
		hostAliases:  []sessionspec.HostAlias{{Domain: "old.example.test", IP: "10.0.0.8"}},
	}
	if err := runtime.UpdateDNSNamespace(context.Background(), "observability"); !errors.Is(err, dnsFailure) {
		t.Fatalf("DNS update error = %v", err)
	}
	if err := runtime.UpdateHostAliases(context.Background(), []sessionspec.HostAlias{
		{Domain: "new.example.test", IP: "10.0.0.9"},
	}); !errors.Is(err, hostsFailure) {
		t.Fatalf("host alias update error = %v", err)
	}
	if runtime.dnsNamespace != "development" || len(runtime.hostAliases) != 1 ||
		runtime.hostAliases[0].Domain != "old.example.test" {
		t.Fatalf(
			"failed updates changed cached settings: namespace=%q aliases=%#v",
			runtime.dnsNamespace,
			runtime.hostAliases,
		)
	}
}

func TestRuntimeStoresNormalizedNetworkSettingsBeforeTUNStarts(t *testing.T) {
	runtime := &Runtime{session: remote.Session{Namespace: "payments"}}
	if err := runtime.UpdateDNSNamespace(context.Background(), "  observability  "); err != nil {
		t.Fatal(err)
	}
	if err := runtime.UpdateHostAliases(context.Background(), []sessionspec.HostAlias{
		{Domain: "API.Example.Test.", IP: "10.0.0.9"},
	}); err != nil {
		t.Fatal(err)
	}
	if runtime.dnsNamespace != "observability" || len(runtime.hostAliases) != 1 ||
		runtime.hostAliases[0].Domain != "api.example.test" {
		t.Fatalf("cached settings = namespace=%q aliases=%#v", runtime.dnsNamespace, runtime.hostAliases)
	}
	if err := runtime.UpdateHostAliases(context.Background(), []sessionspec.HostAlias{
		{Domain: "bad domain", IP: "10.0.0.9"},
	}); err == nil {
		t.Fatal("invalid host alias was cached")
	}
}

func TestRuntimeDiagnosticsIncludeSOCKSAndTUNLogs(t *testing.T) {
	runtime := &Runtime{socksLogs: []string{"12:00:00 [SOCKS] listening on 127.0.0.1:1080"}}
	logs, err := runtime.Logs(context.Background())
	if err != nil || !reflect.DeepEqual(logs, runtime.socksLogs) {
		t.Fatalf("SOCKS logs = %#v, %v", logs, err)
	}
	if _, err := runtime.ConfigJSON(); err == nil {
		t.Fatal("config succeeded without TUN")
	}
	logsFailure := errors.New("logs failed")
	runtime.tun = &testCore{done: make(chan struct{}), logsErr: logsFailure}
	if _, err := runtime.Logs(context.Background()); !errors.Is(err, logsFailure) {
		t.Fatalf("logs error = %v", err)
	}
	runtime.tun = &testCore{done: make(chan struct{})}
	logs, err = runtime.Logs(context.Background())
	wantLogs := []string{"12:00:00 [SOCKS] listening on 127.0.0.1:1080", "[TUN] ready"}
	if err != nil || !reflect.DeepEqual(logs, wantLogs) {
		t.Fatalf("combined logs = %#v, %v; want %#v", logs, err, wantLogs)
	}
	config, err := runtime.ConfigJSON()
	if err != nil || string(config) != "{\"version\":2}" {
		t.Fatalf("config = %q, %v", config, err)
	}
}

func TestRuntimeCloseReportsAllErrorsAndClosesResourcesOnce(t *testing.T) {
	coreFailure := errors.New("close TUN")
	bridgeFailure := errors.New("close bridge")
	controlFailure := errors.New("close control")
	forwarderFailure := errors.New("close forwarder")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	client, peer := net.Pipe()
	defer checkTestClose(t, peer.Close)
	ctx, cancel := context.WithCancel(context.Background())
	core := &testCore{done: make(chan struct{}), closeErr: coreFailure}
	bridge := &testBridge{address: testAddress("127.0.0.1:45010"), closeErr: bridgeFailure}
	control := &testCloseConn{Conn: client, closeErr: controlFailure}
	forwarder := &testForwarder{Listener: listener, closeErr: forwarderFailure}
	runtime := &Runtime{
		ctx: ctx, cancel: cancel, tun: core, bridge: bridge, control: control, forwarder: forwarder,
		done: make(chan struct{}), transportDone: make(chan struct{}),
	}
	closeErr := runtime.Close()
	for _, expected := range []error{coreFailure, bridgeFailure, controlFailure, forwarderFailure} {
		if !errors.Is(closeErr, expected) {
			t.Fatalf("close error %v does not contain %v", closeErr, expected)
		}
	}
	secondCloseErr := runtime.Close()
	for _, expected := range []error{coreFailure, bridgeFailure, controlFailure, forwarderFailure} {
		if !errors.Is(secondCloseErr, expected) {
			t.Fatalf("second close error %v does not contain %v", secondCloseErr, expected)
		}
	}
	if forwarderCloseCalls := forwarder.closeCalls.Load(); core.closeCalls != 1 || bridge.closeCalls != 1 ||
		control.closeCalls != 1 ||
		forwarderCloseCalls != 1 {
		t.Fatalf(
			"close calls: core=%d bridge=%d control=%d forwarder=%d",
			core.closeCalls,
			bridge.closeCalls,
			control.closeCalls,
			forwarderCloseCalls,
		)
	}
	select {
	case <-runtime.Done():
	default:
		t.Fatal("runtime done signal remained open")
	}
}

func TestRuntimeCloseWaitsForTransportWatchers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bridgeFailure := errors.New("close bridge")
	runtime := &Runtime{
		ctx: ctx, cancel: cancel,
		bridge: &testBridge{address: testAddress("127.0.0.1:45010"), closeErr: bridgeFailure},
		done:   make(chan struct{}), transportDone: make(chan struct{}),
	}
	runtime.transportWG.Add(1)
	closed := make(chan error, 2)
	go func() { closed <- runtime.Close() }()
	go func() { closed <- runtime.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned before transport watcher completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	runtime.transportWG.Done()
	for range 2 {
		select {
		case err := <-closed:
			if !errors.Is(err, bridgeFailure) {
				t.Fatalf("concurrent Close error = %v, want %v", err, bridgeFailure)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Close did not wait for transport watcher")
		}
	}
}

func TestRuntimeRejectsTUNStartWhileClosing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	starter := &testTUNStarter{}
	runtime := &Runtime{
		ctx: ctx, cancel: cancel, tunStarter: starter,
		bridge: &testBridge{address: testAddress("127.0.0.1:45010")},
		done:   make(chan struct{}), transportDone: make(chan struct{}),
	}
	runtime.transportWG.Add(1)
	closed := make(chan error, 1)
	go func() { closed <- runtime.Close() }()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.ctx.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if runtime.ctx.Err() == nil {
		t.Fatal("Close did not cancel Runtime context")
	}
	if _, err := runtime.StartTUN(context.Background()); err == nil {
		t.Fatal("StartTUN succeeded while Runtime was closing")
	}
	starts, _, _, _, _ := starter.snapshot()
	if starts != 0 {
		t.Fatalf("TUN starts while closing=%d", starts)
	}
	runtime.transportWG.Done()
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
}
