package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
	"github.com/fengqi-dev/kube-loop/internal/store"
)

func (m *Manager) ManualNetwork(contextName string) cluster.ManualNetwork {
	if m.store == nil || contextName == "" {
		return cluster.ManualNetwork{}
	}
	item := m.store.ManualNetwork(contextName)
	return cluster.ManualNetwork{
		PodCIDRs:       item.PodCIDRs,
		ServiceCIDRs:   item.ServiceCIDRs,
		DNSServer:      item.DNSServer,
		ClusterDomains: item.ClusterDomains,
		DNSNamespace:   item.DNSNamespace,
	}
}

func (m *Manager) SetManualNetwork(contextName string, network cluster.ManualNetwork) error {
	if m.store == nil {
		err := errors.New("state store is unavailable")
		m.AppendLog("ERROR", "save manual network: "+err.Error())
		return err
	}
	if contextName == "" {
		err := errors.New("context is required")
		m.AppendLog("ERROR", "save manual network: "+err.Error())
		return err
	}
	normalized, err := cluster.NormalizeManualNetwork(network)
	if err != nil {
		m.AppendLog("ERROR", fmt.Sprintf("validate manual network for %s: %v", contextName, err))
		return err
	}
	if err := m.store.SetManualNetwork(contextName, store.ManualNetwork{
		PodCIDRs:       normalized.PodCIDRs,
		ServiceCIDRs:   normalized.ServiceCIDRs,
		DNSServer:      normalized.DNSServer,
		ClusterDomains: normalized.ClusterDomains,
		DNSNamespace:   normalized.DNSNamespace,
	}); err != nil {
		m.AppendLog("ERROR", fmt.Sprintf("save manual network for %s: %v", contextName, err))
		return err
	}
	m.AppendLog("INFO", fmt.Sprintf(
		"manual network saved for %s: podCIDRs=%d serviceCIDRs=%d domains=%d dnsNamespace=%s",
		contextName, len(normalized.PodCIDRs), len(normalized.ServiceCIDRs),
		len(normalized.ClusterDomains), normalized.DNSNamespace,
	))
	return nil
}

// SetDNSNamespace updates short-name search namespace for the active tunnel.
func (m *Manager) SetDNSNamespace(contextName, namespace string) error {
	namespace = strings.TrimSpace(namespace)
	if contextName == "" {
		err := errors.New("context is required")
		m.AppendLog("ERROR", "set DNS search namespace: "+err.Error())
		return err
	}
	if namespace == "" {
		namespace = "default"
	}
	normalized, err := cluster.NormalizeManualNetwork(cluster.ManualNetwork{DNSNamespace: namespace})
	if err != nil {
		m.AppendLog("ERROR", fmt.Sprintf(
			"validate DNS search namespace for %s: %v", contextName, err,
		))
		return err
	}
	namespace = normalized.DNSNamespace
	if namespace == "" {
		namespace = "default"
	}
	if m.store != nil {
		current := m.ManualNetwork(contextName)
		current.DNSNamespace = namespace
		if err := m.SetManualNetwork(contextName, current); err != nil {
			return err
		}
	}
	m.mu.Lock()
	core := m.runningCore
	m.mu.Unlock()
	state := m.State()
	if core == nil || state.Phase != PhaseConnected || state.Context != contextName {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := core.UpdateDNSNamespace(ctx, namespace); err != nil {
		m.AppendLog("ERROR", fmt.Sprintf(
			"update active DNS search namespace for %s: %v", contextName, err,
		))
		return err
	}
	m.stateHub.mu.Lock()
	next := m.stateHub.state
	next.DNSNamespace = namespace
	next.DNSWarning = ""
	m.stateHub.state = next
	m.stateHub.mu.Unlock()
	m.publish(next)
	m.AppendLog("INFO", "DNS search namespace set to "+namespace)
	m.probeClusterDNS(ctx, next, core)
	return nil
}

func (m *Manager) probeClusterDNS(parent context.Context, state State, core singbox.RunningCore) {
	if core == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	if err := core.ProbeClusterDNS(ctx); err != nil {
		warning := "Cluster DNS probe failed; split DNS may be overridden by another proxy (for example Clash Verge TUN/system DNS). Try disabling the other client's TUN DNS or reconnect KubeLoop last."
		m.stateHub.mu.Lock()
		next := m.stateHub.state
		if next.Phase == PhaseConnected && next.Context == state.Context {
			next.DNSWarning = warning
			m.stateHub.state = next
			m.stateHub.mu.Unlock()
			m.publish(next)
			m.AppendLog("WARN", warning+": "+err.Error())
			return
		}
		m.stateHub.mu.Unlock()
	}
}

func (m *Manager) HostAliases(contextName string) []store.HostAliasSpec {
	if m.store == nil || contextName == "" {
		return nil
	}
	return m.store.HostAliases(contextName)
}

// SetHostAliases replaces host aliases for a context. An empty list clears stored config.
func (m *Manager) SetHostAliases(contextName string, items []store.HostAliasSpec) error {
	if m.store == nil {
		err := errors.New("state store is unavailable")
		m.AppendLog("ERROR", "save host aliases: "+err.Error())
		return err
	}
	if contextName == "" {
		err := errors.New("context is required")
		m.AppendLog("ERROR", "save host aliases: "+err.Error())
		return err
	}
	normalized, err := normalizeHostAliasSpecs(items)
	if err != nil {
		m.AppendLog("ERROR", fmt.Sprintf("validate host aliases for %s: %v", contextName, err))
		return err
	}
	if err := m.store.SetHostAliases(contextName, normalized); err != nil {
		m.AppendLog("ERROR", fmt.Sprintf("save host aliases for %s: %v", contextName, err))
		return err
	}
	m.mu.Lock()
	core := m.runningCore
	m.mu.Unlock()
	state := m.State()
	if core != nil && state.Phase == PhaseConnected && state.Context == contextName {
		hosts := make([]singbox.HostAlias, 0, len(normalized))
		for _, item := range normalized {
			hosts = append(hosts, singbox.HostAlias{Domain: item.Domain, IP: item.IP})
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := core.UpdateHostAliases(ctx, hosts); err != nil {
			m.AppendLog("ERROR", fmt.Sprintf(
				"update active host aliases for %s: %v", contextName, err,
			))
			return err
		}
	}
	m.AppendLog("INFO", fmt.Sprintf(
		"host aliases saved for %s: entries=%d", contextName, len(normalized),
	))
	return nil
}

func (m *Manager) hostAliasesFor(contextName string) []singbox.HostAlias {
	items := m.HostAliases(contextName)
	if len(items) == 0 {
		return nil
	}
	out := make([]singbox.HostAlias, 0, len(items))
	for _, item := range items {
		out = append(out, singbox.HostAlias{Domain: item.Domain, IP: item.IP})
	}
	return out
}

func normalizeHostAliasSpecs(items []store.HostAliasSpec) ([]store.HostAliasSpec, error) {
	if len(items) == 0 {
		return nil, nil
	}
	converted := make([]singbox.HostAlias, 0, len(items))
	for _, item := range items {
		converted = append(converted, singbox.HostAlias{Domain: item.Domain, IP: item.IP})
	}
	normalized, err := singbox.NormalizeHostAliases(converted)
	if err != nil {
		return nil, err
	}
	out := make([]store.HostAliasSpec, 0, len(normalized))
	for _, item := range normalized {
		out = append(out, store.HostAliasSpec{Domain: item.Domain, IP: item.IP})
	}
	return out, nil
}

func (m *Manager) GatewayInstallManifest() string {
	return cluster.GatewayInstallManifest(m.gatewayImage)
}
