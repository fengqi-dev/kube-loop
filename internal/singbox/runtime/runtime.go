package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/dnsname"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

const startPortCollisionAttempts = 3

type Runtime struct {
	HTTPClient          *http.Client
	PrivilegedStart     singbox.PrivilegedStartFunc
	PrivilegedUpdateDNS singbox.PrivilegedUpdateDNSFunc
}

func (r *Runtime) Start(
	ctx context.Context,
	network singbox.NetworkSpec,
	bridgeAddress string,
	namespace string,
	hosts []singbox.HostAlias,
) (singbox.RunningCore, error) {
	return startWithPortCollisionRetry(ctx, func() (singbox.RunningCore, error) {
		return r.startOnce(ctx, network, bridgeAddress, namespace, hosts)
	})
}

func startWithPortCollisionRetry(
	ctx context.Context,
	start func() (singbox.RunningCore, error),
) (singbox.RunningCore, error) {
	var lastErr error
	for attempt := 1; attempt <= startPortCollisionAttempts; attempt++ {
		core, err := start()
		if err == nil {
			return core, nil
		}
		lastErr = err
		if ctx.Err() != nil || !isAddressAlreadyInUse(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf(
		"start sing-box after %d port allocation attempts: %w",
		startPortCollisionAttempts,
		lastErr,
	)
}

func isAddressAlreadyInUse(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "address already in use") ||
		strings.Contains(message, "only one usage of each socket address")
}

func (r *Runtime) startOnce(
	ctx context.Context,
	network singbox.NetworkSpec,
	bridgeAddress string,
	namespace string,
	hosts []singbox.HostAlias,
) (singbox.RunningCore, error) {
	host, rawPort, err := net.SplitHostPort(bridgeAddress)
	if err != nil {
		return nil, fmt.Errorf("parse SOCKS bridge address: %w", err)
	}
	bridgePort, err := strconv.Atoi(rawPort)
	if err != nil {
		return nil, fmt.Errorf("parse SOCKS bridge port: %w", err)
	}
	controllerPort, err := availablePort()
	if err != nil {
		return nil, err
	}
	// Public port is advertised to the OS (/etc/resolver). sing-box dns-in uses
	// an internal port; dnsSearchProxy expands short names then forwards.
	publicDNSPort, err := selectDNSPort()
	if err != nil {
		return nil, err
	}
	internalDNSPort, err := availableTCPUDPPort()
	if err != nil {
		return nil, err
	}
	secret, err := randomSecret()
	if err != nil {
		return nil, err
	}
	trafficPorts, err := availableTrafficPorts(
		bridgePort, controllerPort, publicDNSPort, internalDNSPort,
	)
	if err != nil {
		return nil, err
	}
	trafficPassword, err := randomSecret()
	if err != nil {
		return nil, err
	}
	tunAddress, err := selectTUNAddress()
	if err != nil {
		return nil, err
	}
	normalizedHosts, err := singbox.NormalizeHostAliases(hosts)
	if err != nil {
		return nil, err
	}
	clusterDomains, _ := dnsname.NormalizeClusterDomains(network.ClusterDomains)
	dnsNamespace := namespace
	if dnsNamespace == "" {
		dnsNamespace = "default"
	}
	spec := singbox.SessionSpec{
		ID:               "session-" + secret[:16],
		PodCIDRs:         network.PodCIDRs,
		ServiceCIDRs:     network.ServiceCIDRs,
		ServiceIPs:       network.ServiceIPs,
		ClusterDNSServer: network.DNSServer,
		ClusterDomains:   clusterDomains,
		BridgeHost:       host,
		BridgePort:       bridgePort,
		ControllerPort:   controllerPort,
		ControllerSecret: secret,
		DNSHost:          singbox.DefaultDNSListen,
		DNSPort:          internalDNSPort,
		PublicDNSPort:    publicDNSPort,
		TUNAddress:       tunAddress,
		Namespace:        namespace,
		DNSNamespace:     dnsNamespace,
		Namespaces:       slices.Clone(network.Namespaces),
		Hosts:            normalizedHosts,
		TrafficPorts:     trafficPorts,
		TrafficPassword:  trafficPassword,
	}
	if r.PrivilegedStart == nil {
		return nil, errors.New("privileged helper is required")
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	config, err := spec.GenerateConfig()
	if err != nil {
		return nil, fmt.Errorf("generate sing-box config: %w", err)
	}
	meta, err := spec.DNS()
	if err != nil {
		return nil, err
	}
	searchDomains := meta.Search
	resolverDomains := meta.Domains
	dnsProxy, err := startDNSSearchProxy(
		singbox.DefaultDNSListen, publicDNSPort, singbox.DefaultDNSListen, internalDNSPort,
		searchDomains, clusterDomains...,
	)
	if err != nil {
		return nil, err
	}
	process := &Process{
		done: make(chan struct{}), stopCh: make(chan struct{}),
		controllerAddress: net.JoinHostPort("127.0.0.1", strconv.Itoa(controllerPort)),
		controllerSecret:  secret,
		dnsPort:           publicDNSPort,
		internalDNSPort:   internalDNSPort,
		resolverDomains:   resolverDomains,
		dnsProxy:          dnsProxy,
		httpClient:        r.HTTPClient,
		trafficEndpoints:  trafficEndpoints(trafficPorts, trafficPassword),
		config:            config,
		spec:              spec,
		updateDNS:         r.PrivilegedUpdateDNS,
	}
	stop, startErr := r.PrivilegedStart(ctx, spec)
	if startErr != nil {
		_ = dnsProxy.Close()
		return nil, startErr
	}
	process.helperStop = stop
	go process.wait()
	go func() {
		select {
		case <-ctx.Done():
			_ = process.Close()
		case <-process.Done():
		}
	}()
	if err := r.waitReady(ctx, process); err != nil {
		_ = process.Close()
		return nil, err
	}
	return process, nil
}

func (r *Runtime) waitReady(ctx context.Context, process *Process) error {
	// Linux auto_redirect/nftables cleanup after a previous session can delay
	// clash API readiness beyond a tight 15s budget on busy CI runners.
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		response, requestErr := process.request(ctx, "/")
		if requestErr == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
			response.Body.Close()
			if response.StatusCode == http.StatusOK || response.StatusCode == http.StatusNotFound {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-process.Done():
			if err := process.Err(); err != nil {
				return err
			}
			return errors.New("sing-box exited before becoming ready")
		case <-deadline.C:
			return errors.New("timed out waiting for sing-box controller")
		case <-ticker.C:
		}
	}
}
