package networkdiag

import (
	"fmt"
	"net/netip"
	"runtime"
	"sort"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
)

type Issue struct {
	Code      string   `json:"code"`
	Severity  Severity `json:"severity"`
	Message   string   `json:"message"`
	Target    string   `json:"target,omitempty"`
	Conflict  string   `json:"conflict,omitempty"`
	Interface string   `json:"interface,omitempty"`
}

type Result struct {
	RoutingMode string  `json:"routingMode"`
	StrictRoute bool    `json:"strictRoute"`
	Issues      []Issue `json:"issues"`
}

type hostRoute struct {
	Prefix    netip.Prefix
	Interface string
	Metric    uint32
}

func Inspect(podCIDRs, serviceCIDRs, serviceIPs []string) Result {
	return inspect(podCIDRs, nil, serviceCIDRs, serviceIPs)
}

// InspectNetworkSpec checks only local host state. It never reads kubeconfig or
// calls Kubernetes; the Spec is the signed remote Session snapshot.
func InspectNetworkSpec(spec networkspec.Spec) Result {
	return inspect(spec.PodCIDRs, spec.PodIPs, spec.ServiceCIDRs, spec.ServiceIPs)
}

func inspect(podCIDRs, podIPs, serviceCIDRs, serviceIPs []string) Result {
	diagnostics := Result{
		RoutingMode: "native",
		StrictRoute: runtime.GOOS != "windows",
	}
	routes, err := readHostRoutes()
	if err != nil {
		diagnostics.Issues = append(diagnostics.Issues, Issue{
			Code:     "route_inspection_failed",
			Severity: SeverityInfo,
			Message:  "Could not inspect existing host routes: " + err.Error(),
		})
	} else {
		diagnostics.Issues = analyzeRouteConflicts(discoveryRoutes(podCIDRs, podIPs, serviceCIDRs, serviceIPs), routes)
	}
	if issue := inspectDNSPort(); issue != nil {
		diagnostics.Issues = append(diagnostics.Issues, *issue)
	}
	return diagnostics
}

func discoveryRoutes(podCIDRs, podIPs, serviceCIDRs, serviceIPs []string) []netip.Prefix {
	values := append([]string{}, podCIDRs...)
	for _, ip := range podIPs {
		if address, err := netip.ParseAddr(ip); err == nil {
			values = append(values, netip.PrefixFrom(address, address.BitLen()).String())
		}
	}
	if len(serviceCIDRs) > 0 {
		values = append(values, serviceCIDRs...)
	} else {
		for _, ip := range serviceIPs {
			if address, err := netip.ParseAddr(ip); err == nil {
				values = append(values, netip.PrefixFrom(address, address.BitLen()).String())
			}
		}
	}
	seen := make(map[netip.Prefix]struct{}, len(values))
	result := make([]netip.Prefix, 0, len(values))
	for _, raw := range values {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			continue
		}
		prefix = prefix.Masked()
		if _, ok := seen[prefix]; ok {
			continue
		}
		seen[prefix] = struct{}{}
		result = append(result, prefix)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].String() < result[j].String()
	})
	return result
}

func analyzeRouteConflicts(targets []netip.Prefix, existing []hostRoute) []Issue {
	issues := make([]Issue, 0)
	seen := make(map[string]struct{})
	for _, target := range targets {
		target = target.Masked()
		for _, route := range existing {
			route.Prefix = route.Prefix.Masked()
			if target.Addr().BitLen() != route.Prefix.Addr().BitLen() {
				continue
			}
			if route.Prefix.Bits() < target.Bits() || !target.Contains(route.Prefix.Addr()) {
				continue
			}
			key := target.String() + "|" + route.Prefix.String() + "|" + route.Interface
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			message := fmt.Sprintf(
				"Cluster route %s overlaps existing route %s",
				target, route.Prefix,
			)
			if route.Interface != "" {
				message += " on " + route.Interface
			}
			issues = append(issues, Issue{
				Code:      "route_overlap",
				Severity:  SeverityWarning,
				Message:   message,
				Target:    target.String(),
				Conflict:  route.Prefix.String(),
				Interface: route.Interface,
			})
		}
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Target != issues[j].Target {
			return issues[i].Target < issues[j].Target
		}
		if issues[i].Conflict != issues[j].Conflict {
			return issues[i].Conflict < issues[j].Conflict
		}
		return issues[i].Interface < issues[j].Interface
	})
	return issues
}
