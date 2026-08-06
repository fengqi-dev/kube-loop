package cluster

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/dnsname"
	"github.com/fengqi-dev/kube-loop/internal/podssh"
	"github.com/miekg/dns"
)

func (p *Provider) discoverGatewayDNS(
	ctx context.Context,
	contextName string,
) (string, []string, error) {
	gateway, err := p.GetGateway(ctx, contextName)
	if err != nil {
		return "", nil, err
	}
	var stdout bytes.Buffer
	err = p.Exec(ctx, podssh.Target{
		Context: contextName, Namespace: GatewayNamespace,
		Pod: gateway.Name, Container: "gateway",
	}, []string{"/kube-loop-gateway", "--print-resolv-conf"}, podssh.Streams{Stdout: &stdout})
	if err != nil {
		return "", nil, err
	}
	return parseClusterDNSConfig(stdout.String())
}

func parseClusterDNSConfig(raw string) (string, []string, error) {
	config, err := dns.ClientConfigFromReader(strings.NewReader(raw))
	if err != nil {
		return "", nil, fmt.Errorf("parse cluster resolv.conf: %w", err)
	}
	server := ""
	for _, rawServer := range config.Servers {
		if address, parseErr := netip.ParseAddr(strings.TrimSpace(rawServer)); parseErr == nil {
			server = address.Unmap().String()
			break
		}
	}

	var discovered []string
	for _, rawSearch := range config.Search {
		search := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(rawSearch)), ".")
		if _, domain, found := strings.Cut(search, ".svc."); found {
			discovered = append(discovered, domain)
		} else if domain, found := strings.CutPrefix(search, "svc."); found {
			discovered = append(discovered, domain)
		}
	}
	domains, err := dnsname.NormalizeClusterDomains(discovered)
	if err != nil {
		return server, nil, fmt.Errorf("normalize cluster domains: %w", err)
	}
	return server, domains, nil
}
