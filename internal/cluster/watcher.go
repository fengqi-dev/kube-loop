package cluster

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/netip"
	"slices"

	clusterinventory "github.com/fengqi-dev/kube-loop/internal/cluster/inventory"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

// InventorySnapshot is the live Pod/Service/Deployment inventory used by the UI.
type InventorySnapshot struct {
	Pods         int
	Services     int
	Deployments  int
	ServiceIPs   []string
	DNSServer    string
	PodItems     []PodInfo
	ServiceItems []ServiceInfo
}

// WatchInventory starts shared informers for Pods, Services, and Deployments.
// namespaces empty = all namespaces; otherwise one factory per namespace.
func (p *Provider) WatchInventory(
	ctx context.Context,
	contextName string,
	namespaces []string,
	onChange func(InventorySnapshot),
) (io.Closer, error) {
	if onChange == nil {
		return nil, fmt.Errorf("inventory callback is required")
	}
	client, err := p.client(contextName)
	if err != nil {
		return nil, err
	}
	return clusterinventory.Watch(ctx, client, namespaces, func(snapshot clusterinventory.Snapshot) {
		onChange(snapshotFromLists(snapshot.Pods, snapshot.Services, snapshot.Deployments))
	})
}

func snapshotFromLists(
	pods []*corev1.Pod,
	services []*corev1.Service,
	deployments []*appsv1.Deployment,
) InventorySnapshot {
	serviceIPs := make(map[string]struct{})
	dnsServer := ""
	for _, service := range services {
		for _, raw := range service.Spec.ClusterIPs {
			if ip, err := netip.ParseAddr(raw); err == nil {
				serviceIPs[ip.String()] = struct{}{}
			}
		}
		if service.Namespace == "kube-system" &&
			(service.Name == "kube-dns" || service.Name == "coredns") {
			for _, raw := range service.Spec.ClusterIPs {
				if ip, err := netip.ParseAddr(raw); err == nil {
					dnsServer = ip.String()
					break
				}
			}
		}
	}
	ips := slices.Sorted(maps.Keys(serviceIPs))
	return InventorySnapshot{
		Pods:         len(pods),
		Services:     len(services),
		Deployments:  len(deployments),
		ServiceIPs:   ips,
		DNSServer:    dnsServer,
		PodItems:     podInfosFromList(pods),
		ServiceItems: serviceInfosFromList(services),
	}
}
