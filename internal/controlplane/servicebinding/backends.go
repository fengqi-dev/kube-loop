package servicebinding

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
)

// BackendTarget is an original Service backend captured before an intercept.
// It is deliberately derived from the rollback snapshot, never from desktop
// input, so Mirror cannot be used as an arbitrary cluster-network dialer.
type BackendTarget struct {
	Address string `json:"address"`
	Port    int32  `json:"port"`
}

// BackendSet contains the ready original targets for one selected Service port.
type BackendSet struct {
	Name        string          `json:"name,omitempty"`
	ServicePort int32           `json:"servicePort"`
	Protocol    corev1.Protocol `json:"protocol"`
	Targets     []BackendTarget `json:"targets"`
}

// ResolveSnapshotBackends derives authoritative primary Mirror targets from a
// captured EndpointSlice or legacy Endpoints snapshot. Every selected Service
// port must have at least one ready backend before the Service may be mutated.
func ResolveSnapshotBackends(snapshot ServiceInterceptSnapshot) ([]BackendSet, error) {
	if len(snapshot.Ports) == 0 {
		return nil, errors.New("service snapshot contains no selected ports")
	}
	sets := make([]BackendSet, 0, len(snapshot.Ports))
	for _, selected := range snapshot.Ports {
		protocol := normalizedProtocol(selected.Protocol)
		set := BackendSet{
			Name: selected.Name, ServicePort: selected.ServicePort, Protocol: protocol,
		}
		seen := make(map[string]struct{})
		if snapshot.HasEndpointSlices {
			for _, endpointSlice := range snapshot.EndpointSlices {
				targetPort, ok := matchSlicePort(endpointSlice.Ports, selected, protocol)
				if !ok {
					continue
				}
				for _, endpoint := range endpointSlice.Endpoints {
					if !readyEndpoint(endpoint) {
						continue
					}
					for _, address := range endpoint.Addresses {
						appendBackend(&set.Targets, seen, address, targetPort)
					}
				}
			}
		} else if snapshot.HasEndpoints {
			for _, subset := range snapshot.EndpointsSubsets {
				targetPort, ok := matchLegacyPort(subset.Ports, selected, protocol)
				if !ok {
					continue
				}
				for _, address := range subset.Addresses {
					appendBackend(&set.Targets, seen, address.IP, targetPort)
				}
			}
		}
		if len(set.Targets) == 0 {
			return nil, fmt.Errorf(
				"service port %s/%d/%s has no ready original backend",
				selected.Name, selected.ServicePort, strings.ToLower(string(protocol)),
			)
		}
		slices.SortFunc(set.Targets, func(left, right BackendTarget) int {
			if comparison := strings.Compare(left.Address, right.Address); comparison != 0 {
				return comparison
			}
			return int(left.Port - right.Port)
		})
		sets = append(sets, set)
	}
	slices.SortFunc(sets, func(left, right BackendSet) int {
		if left.ServicePort != right.ServicePort {
			return int(left.ServicePort - right.ServicePort)
		}
		return strings.Compare(string(left.Protocol), string(right.Protocol))
	})
	return sets, nil
}

func matchSlicePort(
	ports []discoveryv1.EndpointPort,
	selected InterceptPort,
	protocol corev1.Protocol,
) (int32, bool) {
	candidates := make([]discoveryv1.EndpointPort, 0, len(ports))
	for _, port := range ports {
		if port.Port == nil || *port.Port < 1 || *port.Port > 65535 ||
			normalizedProtocol(dereferenceProtocol(port.Protocol)) != protocol {
			continue
		}
		name := ""
		if port.Name != nil {
			name = *port.Name
		}
		if selected.Name != "" {
			if name == selected.Name {
				return *port.Port, true
			}
			continue
		}
		if name == "" {
			candidates = append(candidates, port)
		}
	}
	if len(candidates) == 1 {
		return *candidates[0].Port, true
	}
	for _, port := range candidates {
		if *port.Port == selected.ServicePort {
			return *port.Port, true
		}
	}
	return 0, false
}

func matchLegacyPort(
	ports []corev1.EndpointPort,
	selected InterceptPort,
	protocol corev1.Protocol,
) (int32, bool) {
	candidates := make([]corev1.EndpointPort, 0, len(ports))
	for _, port := range ports {
		if port.Port < 1 || port.Port > 65535 || normalizedProtocol(port.Protocol) != protocol {
			continue
		}
		if selected.Name != "" {
			if port.Name == selected.Name {
				return port.Port, true
			}
			continue
		}
		if port.Name == "" {
			candidates = append(candidates, port)
		}
	}
	if len(candidates) == 1 {
		return candidates[0].Port, true
	}
	for _, port := range candidates {
		if port.Port == selected.ServicePort {
			return port.Port, true
		}
	}
	return 0, false
}

func readyEndpoint(endpoint discoveryv1.Endpoint) bool {
	if endpoint.Conditions.Ready != nil && !*endpoint.Conditions.Ready {
		return false
	}
	return endpoint.Conditions.Terminating == nil || !*endpoint.Conditions.Terminating
}

func appendBackend(targets *[]BackendTarget, seen map[string]struct{}, raw string, port int32) {
	address, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return
	}
	address = address.Unmap()
	if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() {
		return
	}
	key := address.String() + "/" + strconv.Itoa(int(port))
	if _, exists := seen[key]; exists {
		return
	}
	seen[key] = struct{}{}
	*targets = append(*targets, BackendTarget{Address: address.String(), Port: port})
}

func normalizedProtocol(protocol corev1.Protocol) corev1.Protocol {
	if protocol == "" {
		return corev1.ProtocolTCP
	}
	return protocol
}

func dereferenceProtocol(protocol *corev1.Protocol) corev1.Protocol {
	if protocol == nil {
		return corev1.ProtocolTCP
	}
	return *protocol
}
