package runtime

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/dnsname"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

func (p *Process) UpdateDNSNamespace(ctx context.Context, namespace string) error {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = defaultNamespace
	}
	return p.applyDNSUpdate(
		ctx,
		func(spec *singbox.SessionSpec) {
			spec.DNSNamespace = namespace
			spec.Namespace = namespace
		},
		func(proxy *dnsSearchProxy, spec singbox.SessionSpec, dns singbox.DNSMeta) {
			proxy.SetSearch(dns.Search)
			proxy.SetClusterDomains(spec.ClusterDomains)
		},
	)
}

func (p *Process) UpdateHostAliases(ctx context.Context, hosts []singbox.HostAlias) error {
	normalized, err := singbox.NormalizeHostAliases(hosts)
	if err != nil {
		return err
	}
	return p.applyDNSUpdate(
		ctx,
		func(spec *singbox.SessionSpec) {
			spec.Hosts = normalized
		},
		func(proxy *dnsSearchProxy, spec singbox.SessionSpec, _ singbox.DNSMeta) {
			proxy.SetHostAliases(spec.Hosts)
		},
	)
}

func (p *Process) applyDNSUpdate(
	ctx context.Context,
	mutate func(*singbox.SessionSpec),
	commitProxy func(*dnsSearchProxy, singbox.SessionSpec, singbox.DNSMeta),
) error {
	p.updateMu.Lock()
	defer p.updateMu.Unlock()

	p.specMu.Lock()
	if p.closed {
		p.specMu.Unlock()
		return errProcessClosed
	}
	nextSpec := p.spec
	mutate(&nextSpec)
	dnsMeta, err := nextSpec.DNS()
	sessionID := nextSpec.ID
	updateDNS := p.updateDNS
	p.specMu.Unlock()
	if err != nil {
		return err
	}
	if updateDNS == nil {
		return errors.New("privileged DNS update is unavailable; reconnect to apply")
	}
	if err := updateDNS(ctx, sessionID, dnsMeta); err != nil {
		return err
	}

	p.specMu.Lock()
	p.spec = nextSpec
	p.resolverDomains = slices.Clone(dnsMeta.Domains)
	proxy := p.dnsProxy
	p.specMu.Unlock()
	if proxy != nil {
		commitProxy(proxy, nextSpec, dnsMeta)
	}
	return nil
}

func (p *Process) ProbeClusterDNS(ctx context.Context) error {
	p.specMu.Lock()
	domains := slices.Clone(p.spec.ClusterDomains)
	port := p.dnsPort
	p.specMu.Unlock()
	if len(domains) == 0 {
		domains = []string{dnsname.DefaultClusterDomain}
	}
	name := "kubernetes.default.svc." + domains[0] + "."
	return probeLocalDNS(ctx, singbox.DefaultDNSListen, port, name)
}
