package cluster

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Capabilities describes what the current kubeconfig identity can do.
type Capabilities struct {
	GatewayInstall     bool     `json:"gatewayInstall"`
	GatewayPortForward bool     `json:"gatewayPortForward"`
	ClusterNodes       bool     `json:"clusterNodes"`
	InventoryCluster   bool     `json:"inventoryCluster"`
	ServiceWrite       bool     `json:"serviceWrite"`
	ServiceCreate      bool     `json:"serviceCreate"`
	PodExec            bool     `json:"podExec"`
	ScopeNamespaces    []string `json:"scopeNamespaces,omitempty"`
	Issues             []string `json:"issues,omitempty"`
}

// ManualNetwork is user-supplied discovery when cluster-wide reads are forbidden.
type ManualNetwork struct {
	PodCIDRs       []string `json:"podCIDRs,omitempty"`
	ServiceCIDRs   []string `json:"serviceCIDRs,omitempty"`
	DNSServer      string   `json:"dnsServer,omitempty"`
	ClusterDomains []string `json:"clusterDomains,omitempty"`
	DNSNamespace   string   `json:"dnsNamespace,omitempty"`
}

func (p *Provider) ProbeCapabilities(ctx context.Context, contextName string) (Capabilities, error) {
	client, err := p.client(contextName)
	if err != nil {
		return Capabilities{}, err
	}
	caps := Capabilities{}

	caps.GatewayInstall = canAccess(ctx, client, authorizationv1.ResourceAttributes{
		Namespace: GatewayNamespace, Group: "apps", Resource: "deployments", Verb: "create",
	}) && canAccess(ctx, client, authorizationv1.ResourceAttributes{
		Namespace: GatewayNamespace, Group: "apps", Resource: "deployments", Verb: "update",
	})

	caps.GatewayPortForward = canAccess(ctx, client, authorizationv1.ResourceAttributes{
		Namespace: GatewayNamespace, Resource: "pods", Subresource: "portforward", Verb: "create",
	})

	caps.ClusterNodes = canAccess(ctx, client, authorizationv1.ResourceAttributes{
		Resource: "nodes", Verb: "list",
	})

	caps.InventoryCluster = canAccess(ctx, client, authorizationv1.ResourceAttributes{
		Resource: "pods", Verb: "list",
	}) && canAccess(ctx, client, authorizationv1.ResourceAttributes{
		Resource: "services", Verb: "list",
	})

	probeNS := "default"
	if names, listErr := p.Namespaces(ctx, contextName); listErr == nil && len(names) > 0 {
		probeNS = names[0]
		if !caps.InventoryCluster {
			caps.ScopeNamespaces = append([]string{}, names...)
		}
	} else if !caps.InventoryCluster {
		for _, candidate := range []string{"default", "dev", "development", "staging", "prod"} {
			if canAccess(ctx, client, authorizationv1.ResourceAttributes{
				Namespace: candidate, Resource: "pods", Verb: "list",
			}) {
				caps.ScopeNamespaces = append(caps.ScopeNamespaces, candidate)
			}
		}
		if len(caps.ScopeNamespaces) > 0 {
			probeNS = caps.ScopeNamespaces[0]
		}
	}

	caps.ServiceWrite = canAccess(ctx, client, authorizationv1.ResourceAttributes{
		Namespace: probeNS, Resource: "services", Verb: "update",
	}) && canAccess(ctx, client, authorizationv1.ResourceAttributes{
		Namespace: probeNS, Group: "discovery.k8s.io", Resource: "endpointslices", Verb: "list",
	}) && canAccess(ctx, client, authorizationv1.ResourceAttributes{
		Namespace: probeNS, Group: "discovery.k8s.io", Resource: "endpointslices", Verb: "get",
	}) && canAccess(ctx, client, authorizationv1.ResourceAttributes{
		Namespace: probeNS, Group: "discovery.k8s.io", Resource: "endpointslices", Verb: "create",
	}) && canAccess(ctx, client, authorizationv1.ResourceAttributes{
		Namespace: probeNS, Group: "discovery.k8s.io", Resource: "endpointslices", Verb: "update",
	}) && canAccess(ctx, client, authorizationv1.ResourceAttributes{
		Namespace: probeNS, Group: "discovery.k8s.io", Resource: "endpointslices", Verb: "delete",
	})

	caps.ServiceCreate = canAccess(ctx, client, authorizationv1.ResourceAttributes{
		Namespace: probeNS, Resource: "services", Verb: "create",
	}) && canAccess(ctx, client, authorizationv1.ResourceAttributes{
		Namespace: probeNS, Resource: "services", Verb: "delete",
	}) && canAccess(ctx, client, authorizationv1.ResourceAttributes{
		Namespace: probeNS, Group: "discovery.k8s.io", Resource: "endpointslices", Verb: "create",
	}) && canAccess(ctx, client, authorizationv1.ResourceAttributes{
		Namespace: probeNS, Group: "discovery.k8s.io", Resource: "endpointslices", Verb: "delete",
	})

	caps.PodExec = canAccess(ctx, client, authorizationv1.ResourceAttributes{
		Namespace: probeNS, Resource: "pods", Subresource: "exec", Verb: "create",
	})

	if !caps.GatewayPortForward {
		caps.Issues = append(caps.Issues, "Missing pods/portforward permission in kubeloop-system (cannot connect)")
	}
	if !caps.GatewayInstall {
		caps.Issues = append(caps.Issues, "No Gateway install permission; will try an admin-preinstalled Gateway")
	}
	if !caps.InventoryCluster && len(caps.ScopeNamespaces) == 0 {
		caps.Issues = append(caps.Issues, "Cannot list Pods/Services in any namespace")
	}
	if !caps.ClusterNodes {
		caps.Issues = append(caps.Issues, "Cannot list Nodes; Pod CIDR may need to be entered manually on Overview")
	}
	if !caps.PodExec {
		caps.Issues = append(caps.Issues, "Missing pods/exec permission (Pod SSH is unavailable)")
	}
	return caps, nil
}

func canAccess(ctx context.Context, client kubernetes.Interface, attrs authorizationv1.ResourceAttributes) bool {
	review := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &attrs},
	}
	result, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return false
	}
	return result.Status.Allowed
}

// FormatForbidden returns a readable message for API Forbidden errors.
func FormatForbidden(err error, hint string) string {
	if err == nil {
		return ""
	}
	if apierrors.IsForbidden(err) {
		if hint != "" {
			return fmt.Sprintf("permission denied: %s (%v)", hint, err)
		}
		return fmt.Sprintf("permission denied: %v", err)
	}
	return err.Error()
}

// MergeManualNetwork fills empty auto-discovery fields from manual values.
func MergeManualNetwork(auto Discovery, manual ManualNetwork) Discovery {
	out := auto
	if len(out.PodCIDRs) == 0 && len(manual.PodCIDRs) > 0 {
		out.PodCIDRs = append([]string{}, manual.PodCIDRs...)
	}
	if len(out.ServiceCIDRs) == 0 && len(manual.ServiceCIDRs) > 0 {
		out.ServiceCIDRs = append([]string{}, manual.ServiceCIDRs...)
	}
	if out.DNSServer == "" && manual.DNSServer != "" {
		out.DNSServer = manual.DNSServer
	}
	if len(out.ClusterDomains) == 0 && len(manual.ClusterDomains) > 0 {
		out.ClusterDomains = append([]string{}, manual.ClusterDomains...)
	}
	domains, err := NormalizeClusterDomains(out.ClusterDomains)
	if err == nil {
		out.ClusterDomains = domains
	} else if len(out.ClusterDomains) == 0 {
		out.ClusterDomains = []string{DefaultClusterDomain}
	}
	return out
}

// NormalizeManualNetwork validates and normalizes user-supplied CIDRs / DNS.
func NormalizeManualNetwork(network ManualNetwork) (ManualNetwork, error) {
	out := ManualNetwork{}
	for _, item := range network.PodCIDRs {
		cidrs, err := parseCIDRList(item)
		if err != nil {
			return ManualNetwork{}, fmt.Errorf("pod CIDR: %w", err)
		}
		out.PodCIDRs = append(out.PodCIDRs, cidrs...)
	}
	for _, item := range network.ServiceCIDRs {
		cidrs, err := parseCIDRList(item)
		if err != nil {
			return ManualNetwork{}, fmt.Errorf("service CIDR: %w", err)
		}
		out.ServiceCIDRs = append(out.ServiceCIDRs, cidrs...)
	}
	dns := strings.TrimSpace(network.DNSServer)
	if dns != "" {
		addr, err := netip.ParseAddr(dns)
		if err != nil {
			return ManualNetwork{}, fmt.Errorf("dns server: invalid IP %q", dns)
		}
		out.DNSServer = addr.String()
	}
	domains, err := NormalizeClusterDomains(network.ClusterDomains)
	if err != nil {
		return ManualNetwork{}, err
	}
	// Persist custom domains only; default alone clears to empty (Merge fills it).
	if len(domains) == 1 && domains[0] == DefaultClusterDomain {
		out.ClusterDomains = nil
	} else {
		out.ClusterDomains = domains
	}
	ns := strings.TrimSpace(network.DNSNamespace)
	if ns != "" {
		if !safeClusterDomain(ns) || strings.Contains(ns, ".") {
			return ManualNetwork{}, fmt.Errorf("invalid DNS namespace %q", network.DNSNamespace)
		}
		out.DNSNamespace = strings.ToLower(ns)
	}
	return out, nil
}

func parseCIDRList(raw string) ([]string, error) {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ';'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q", part)
		}
		out = append(out, prefix.Masked().String())
	}
	return out, nil
}
