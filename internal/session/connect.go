package session

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/intercept"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
	"github.com/fengqi-dev/kube-loop/internal/socksbridge"
	"github.com/fengqi-dev/kube-loop/internal/traffic"
)

func (m *Manager) Connect(parent context.Context, request Request) error {
	if request.Context == "" {
		err := errors.New("context is required")
		m.AppendLog("ERROR", "connection request rejected: "+err.Error())
		return err
	}
	if request.Namespace == "" {
		request.Namespace = "default"
	}
	if request.Mode == "" {
		request.Mode = ConnectionModeTUN
	}
	if request.Mode != ConnectionModeTUN && request.Mode != ConnectionModeSOCKS {
		err := fmt.Errorf("unsupported connection mode %q", request.Mode)
		m.AppendLog("ERROR", "connection request rejected: "+err.Error())
		return err
	}
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		err := errors.New("a connection is already active")
		m.AppendLog("WARN", "connection request rejected: "+err.Error())
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	m.cancel = cancel
	m.done = done
	m.mu.Unlock()

	go m.run(ctx, request, done)
	return nil
}

func (m *Manager) run(ctx context.Context, request Request, done chan struct{}) {
	runtime := newSessionRuntime()
	defer func() {
		if err := runtime.Close(); err != nil {
			m.recordLog("ERROR", fmt.Sprintf("close session runtime: %v", err))
		}
		m.mu.RLock()
		currentRun := m.done == done
		m.mu.RUnlock()
		if currentRun && ctx.Err() != nil {
			current := m.State()
			if current.Phase != PhaseIdle {
				m.clearRecentConnections()
				m.stateHub.mu.Lock()
				m.appendLogLocked("INFO", "disconnected")
				m.stateHub.mu.Unlock()
				m.publish(State{
					Phase:             PhaseIdle,
					Mode:              current.Mode,
					Message:           "Disconnected",
					CoreVersion:       singbox.Version,
					KubernetesVersion: current.KubernetesVersion,
					Context:           current.Context,
					Namespace:         current.Namespace,
				})
			}
		}
		m.mu.Lock()
		if currentRun {
			m.cancel = nil
			m.done = nil
			m.runningCore = nil
		}
		m.mu.Unlock()
		close(done)
	}()

	if ctx.Err() != nil {
		return
	}
	prev := m.State()
	state := State{
		Phase: PhaseChecking, Mode: request.Mode, Context: request.Context, Namespace: request.Namespace,
		Message: "Checking Kubernetes access", CoreVersion: singbox.Version,
		// Keep the last probed version so the Overview subtitle does not flash
		// back to the cluster name while ServerVersion is re-fetched.
		KubernetesVersion: prev.KubernetesVersion,
	}
	m.publish(state)
	m.AppendLog("INFO", fmt.Sprintf("connecting to context %s", request.Context))

	caps, err := m.connection.ProbeCapabilities(ctx, request.Context)
	if err != nil {
		m.fail(ctx, state, "Could not check cluster permissions", err)
		return
	}
	if ctx.Err() != nil {
		return
	}
	state.Capabilities = &caps
	state.ScopeNamespaces = append([]string{}, caps.ScopeNamespaces...)
	if version, versionErr := m.connection.ServerVersion(ctx, request.Context); versionErr == nil {
		state.KubernetesVersion = version
	}
	m.publish(state)
	if state.KubernetesVersion != "" {
		m.AppendLog("INFO", "kubernetes "+state.KubernetesVersion)
	} else {
		m.AppendLog("INFO", "kubernetes version unavailable")
	}
	gatewayMode := "preinstalled"
	if caps.GatewayInstall {
		gatewayMode = "managed"
	}
	inventoryScope := "namespaced"
	if caps.InventoryCluster {
		inventoryScope = "cluster"
	}
	m.AppendLog("INFO", fmt.Sprintf(
		"cluster access verified: gateway=%s inventory=%s",
		gatewayMode, inventoryScope,
	))
	for _, issue := range caps.Issues {
		m.AppendLog("INFO", issue)
	}
	if !caps.GatewayPortForward {
		state.GatewayManifest = cluster.GatewayInstallManifest(m.gatewayImage)
		m.fail(ctx, state, "Current account cannot port-forward to the Gateway", errors.New("missing pods/portforward in kubeloop-system"))
		return
	}

	state.Phase = PhaseInstalling
	state.Message = "Installing or checking the cluster Gateway"
	m.publish(state)
	var gateway cluster.GatewayInfo
	if caps.GatewayInstall {
		gateway, err = m.gateway.EnsureGateway(ctx, request.Context, m.gatewayImage)
		if err != nil {
			m.fail(ctx, state, "Could not install the cluster Gateway", err)
			return
		}
	} else {
		gateway, err = m.gateway.GetGateway(ctx, request.Context)
		if err != nil {
			state.GatewayManifest = cluster.GatewayInstallManifest(m.gatewayImage)
			m.fail(ctx, state, "No preinstalled Gateway found; ask an admin to install it or grant install permission", err)
			return
		}
	}
	if ctx.Err() != nil {
		return
	}
	m.AppendLog("INFO", fmt.Sprintf(
		"gateway ready: pod=%s ip=%s", gateway.Name, gateway.IP,
	))

	state.Phase = PhaseDiscovering
	state.Message = "Discovering Pods, Services, and cluster DNS"
	m.publish(state)
	scopeNS := caps.ScopeNamespaces
	if caps.InventoryCluster {
		scopeNS = nil
	}
	discovery, err := m.connection.Discover(ctx, request.Context, scopeNS)
	if err != nil {
		m.fail(ctx, state, "Could not read cluster network information", err)
		return
	}
	if ctx.Err() != nil {
		return
	}
	dnsNamespace := request.Namespace
	if m.store != nil {
		manual := m.store.ManualNetwork(request.Context)
		discovery = cluster.MergeManualNetwork(discovery, cluster.ManualNetwork{
			PodCIDRs:       manual.PodCIDRs,
			ServiceCIDRs:   manual.ServiceCIDRs,
			DNSServer:      manual.DNSServer,
			ClusterDomains: manual.ClusterDomains,
		})
		if manual.DNSNamespace != "" {
			dnsNamespace = manual.DNSNamespace
		}
	}
	state.Discovery = &discovery
	state.DNSNamespace = dnsNamespace
	if request.Mode == ConnectionModeSOCKS {
		state.Network = &NetworkDiagnostics{
			RoutingMode: "proxy",
			StrictRoute: false,
		}
	} else {
		state.Network = inspectNetwork(discovery)
	}
	m.publish(state)
	m.AppendLog("INFO", fmt.Sprintf(
		"network discovered: podCIDRs=%d serviceCIDRs=%d serviceIPs=%d dns=%s",
		len(discovery.PodCIDRs), len(discovery.ServiceCIDRs), len(discovery.ServiceIPs),
		discovery.DNSServer,
	))
	for _, issue := range state.Network.Issues {
		m.AppendLog("WARN", issue.Message)
	}

	forwarder, err := m.gateway.StartPortForward(
		ctx, request.Context, gateway.Name, cluster.GatewayPort,
	)
	if err != nil {
		m.fail(ctx, state, "Could not establish a secure Gateway channel", err)
		return
	}
	runtime.Add("Gateway port-forward", forwarder)
	m.AppendLog("INFO", "gateway channel established at "+forwarder.Address())
	if ctx.Err() != nil {
		return
	}

	bridgeContext, stopBridge := context.WithCancel(ctx)
	runtime.AddFunc("SOCKS Bridge context", stopBridge)
	bridgeListenAddress := "127.0.0.1:0"
	if request.Mode == ConnectionModeSOCKS {
		bridgeListenAddress = net.JoinHostPort(
			"127.0.0.1", strconv.Itoa(DefaultSOCKSPort),
		)
	}
	bridge, err := m.bridgeFactory(
		bridgeContext, forwarder.Address(), bridgeListenAddress,
	)
	if err != nil && request.Mode == ConnectionModeSOCKS {
		m.AppendLog("WARN", fmt.Sprintf(
			"preferred SOCKS5 port %d is unavailable; selecting another port: %v",
			DefaultSOCKSPort, err,
		))
		bridge, err = m.bridgeFactory(
			bridgeContext, forwarder.Address(), "127.0.0.1:0",
		)
	}
	if err != nil {
		m.fail(ctx, state, "Could not start the local SOCKS Bridge", err)
		return
	}
	runtime.Add("SOCKS Bridge", bridge)
	m.AppendLog("INFO", "local SOCKS bridge listening at "+bridge.Addr().String())
	if ctx.Err() != nil {
		return
	}

	if request.Mode == ConnectionModeSOCKS {
		rawHost, rawPort, splitErr := net.SplitHostPort(bridge.Addr().String())
		if splitErr != nil {
			m.fail(ctx, state, "Could not expose the local SOCKS5 proxy", splitErr)
			return
		}
		if ip := net.ParseIP(rawHost); ip == nil || !ip.IsLoopback() {
			m.fail(ctx, state, "Could not expose the local SOCKS5 proxy", errors.New("SOCKS5 proxy must listen on loopback"))
			return
		}
		socksPort, parseErr := strconv.Atoi(rawPort)
		if parseErr != nil || socksPort < 1 || socksPort > 65535 {
			m.fail(ctx, state, "Could not expose the local SOCKS5 proxy", errors.New("SOCKS5 proxy has an invalid port"))
			return
		}
		// SOCKS5 mode has no sing-box feature inbound, so persisted TCP
		// forwards are restored through the Kubernetes API port-forward API.
		m.restoreNativePortForwards(ctx, request.Context)
		connectedAt := time.Now()
		state.Phase = PhaseConnected
		state.Message = "SOCKS5 proxy connected to the cluster Gateway"
		state.ConnectedAt = &connectedAt
		state.SOCKSPort = socksPort
		state.Metrics = &singbox.Metrics{}
		state.Capabilities = &caps
		state.ScopeNamespaces = append([]string{}, caps.ScopeNamespaces...)
		state.DNSWarning = ""
		m.AppendLog("INFO", fmt.Sprintf(
			"SOCKS5 mode ready at 127.0.0.1:%d; TUN and system proxy are disabled",
			socksPort,
		))
		m.publish(state)
		m.AppendLog("INFO", fmt.Sprintf("connected to context %s in SOCKS5 mode", request.Context))
		m.persistConnectedMode(request)

		inventory, watchErr := m.connection.WatchInventory(
			ctx, request.Context, scopeNS, func(snap cluster.InventorySnapshot) {
				m.applyInventory(snap)
			},
		)
		if watchErr != nil {
			m.fail(ctx, state, "Could not watch cluster resource changes", watchErr)
			return
		}
		runtime.Add("inventory watcher", inventory)
		m.recordLog("INFO", "cluster inventory watcher started")
		<-ctx.Done()
		return
	}

	if err := m.intercept.Start(ctx, request.Context, gateway.IP, forwarder.Address()); err != nil {
		m.fail(ctx, state, "Could not start the Service Intercept control channel", err)
		return
	}
	m.AppendLog("INFO", "service intercept control channel ready")
	var interceptCloseOnce sync.Once
	closeIntercept := closerFunc(func() {
		interceptCloseOnce.Do(func() {
			_ = m.intercept.StopAll(context.Background())
		})
	})
	// Keep an early guard for failures before sing-box starts. The same
	// idempotent closer is appended again after the core so normal teardown
	// restores Kubernetes resources before closing the data plane.
	runtime.Add("early Intercept guard", closeIntercept)
	if ctx.Err() != nil {
		return
	}
	if hostBridge, ok := bridge.(*socksbridge.Bridge); ok {
		hostBridge.SetHostTCPHandler(m.intercept.HostTCP)
		hostBridge.SetHostUDPHandler(m.intercept.HostUDP)
		runtime.AddFunc("SOCKS Bridge host routes", func() {
			hostBridge.SetHostTCPHandler(nil)
			hostBridge.SetHostUDPHandler(nil)
		})
	}

	state.Phase = PhaseStarting
	state.Message = "Installing and starting sing-box TUN"
	m.publish(state)
	hosts := m.hostAliasesFor(request.Context)
	core, err := m.core.Start(
		ctx, networkSpec(discovery), bridge.Addr().String(), dnsNamespace, hosts,
	)
	if err != nil {
		m.fail(ctx, state, "Could not start sing-box TUN", err)
		return
	}
	runtime.Add("sing-box core", core)
	m.AppendLog("INFO", fmt.Sprintf(
		"sing-box TUN started: dnsNamespace=%s hostAliases=%d",
		dnsNamespace, len(hosts),
	))
	if ctx.Err() != nil {
		return
	}
	m.mu.Lock()
	m.runningCore = core
	m.mu.Unlock()
	trafficEndpoints := core.TrafficEndpoints()
	if err := trafficEndpoints.Validate(); err != nil {
		m.fail(ctx, state, "sing-box feature inbounds are unavailable", err)
		return
	}
	tracker := traffic.NewTracker()
	m.mu.Lock()
	m.trafficTracker = tracker
	m.mu.Unlock()
	m.intercept.SetTrafficDialers(intercept.TrafficDialers{
		Exchange:     trackedTrafficDialer(trafficEndpoints.Exchange, singbox.TrafficUserExchange, tracker),
		Preview:      trackedTrafficDialer(trafficEndpoints.Preview, singbox.TrafficUserPreview, tracker),
		MirrorShadow: trackedTrafficDialer(trafficEndpoints.MirrorShadow, singbox.TrafficUserMirrorShadow, tracker),
	})
	m.portfwd.SetTrafficDialer(
		request.Context,
		trackedPortForwardDialer(trafficEndpoints.PortForward, tracker),
	)
	m.AppendLog("INFO", "feature traffic routes ready")
	runtime.AddFunc("feature traffic bindings", func() {
		m.intercept.SetTrafficDialers(intercept.TrafficDialers{})
		m.portfwd.StopRouted()
		m.portfwd.SetTrafficDialer("", nil)
		// A normal disconnect removes routed forwards from the persisted
		// intents. During application shutdown, PersistShutdown already saved
		// those intents for the next launch, so teardown must not erase them.
		if !m.isShuttingDown() {
			m.persistPortForwards()
		}
		m.mu.Lock()
		m.trafficTracker = nil
		m.mu.Unlock()
	})
	runtime.Add("Intercept restore", closeIntercept)

	connectedAt := time.Now()
	state.Phase = PhaseConnected
	state.Message = "Connected; Pods, Services, and cluster DNS are reachable"
	state.ConnectedAt = &connectedAt
	state.SOCKSPort = 0
	state.Metrics = &singbox.Metrics{}
	state.Capabilities = &caps
	state.ScopeNamespaces = append([]string{}, caps.ScopeNamespaces...)
	state.DNSNamespace = dnsNamespace
	state.DNSWarning = ""
	// Restore bindings before publishing Connected so the frontend's first
	// ready-state refresh observes the restored sessions.
	m.restoreBindings(ctx, request.Context)
	m.publish(state)
	m.AppendLog("INFO", fmt.Sprintf("connected to context %s", request.Context))
	m.persistConnectedMode(request)
	m.probeClusterDNS(ctx, state, core)

	inventory, err := m.connection.WatchInventory(ctx, request.Context, scopeNS, func(snap cluster.InventorySnapshot) {
		m.applyInventory(snap)
	})
	if err != nil {
		m.fail(ctx, state, "Could not watch cluster resource changes", err)
		return
	}
	runtime.Add("inventory watcher", inventory)
	m.recordLog("INFO", "cluster inventory watcher started")

	m.serveConnected(ctx, state, core, request.Context, bridge, runtime)
}

func (m *Manager) persistConnectedMode(request Request) {
	if m.store == nil {
		return
	}
	if err := m.store.SetConnectionMode(request.Context, string(request.Mode)); err != nil {
		m.AppendLog("ERROR", fmt.Sprintf("persist connection mode: %v", err))
	}
	if err := m.store.SetConnected(request.Context, request.Namespace, true); err != nil {
		m.AppendLog("ERROR", fmt.Sprintf("persist connected state: %v", err))
	}
}
