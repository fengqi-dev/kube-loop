package session

import (
	"slices"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

func networkSpec(discovery cluster.Discovery) singbox.NetworkSpec {
	return singbox.NetworkSpec{
		PodCIDRs:       slices.Clone(discovery.PodCIDRs),
		ServiceCIDRs:   slices.Clone(discovery.ServiceCIDRs),
		ServiceIPs:     slices.Clone(discovery.ServiceIPs),
		DNSServer:      discovery.DNSServer,
		ClusterDomains: slices.Clone(discovery.ClusterDomains),
	}
}
