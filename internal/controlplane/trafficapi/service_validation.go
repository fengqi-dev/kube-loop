package trafficapi

import (
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/servicemodel"
)

// NormalizeServicePorts normalizes an existing Service name and its requested
// ports in place. Preview has separate rules for creating a new Service.
func NormalizeServicePorts(service *string, ports []servicemodel.Port) *controlplaneapi.Error {
	*service = strings.TrimSpace(*service)
	if len(validation.IsDNS1123Subdomain(*service)) != 0 {
		return controlplaneapi.Invalid("service", "Service name is invalid")
	}
	if len(ports) == 0 || len(ports) > 64 {
		return controlplaneapi.Invalid("ports", "one to 64 Service ports are required")
	}
	seen := make(map[string]struct{}, len(ports))
	for index := range ports {
		port := &ports[index]
		port.Name = strings.TrimSpace(port.Name)
		port.Protocol = strings.ToLower(strings.TrimSpace(port.Protocol))
		if port.ServicePort < 1 || port.ServicePort > 65535 ||
			(port.Protocol != "tcp" && port.Protocol != "udp") {
			return controlplaneapi.Invalid("ports", "Service port and protocol are invalid")
		}
		key := fmt.Sprintf("%s/%d", port.Protocol, port.ServicePort)
		if _, exists := seen[key]; exists {
			return controlplaneapi.Invalid("ports", "Service ports must be unique")
		}
		seen[key] = struct{}{}
	}
	slices.SortFunc(ports, ComparePorts)
	return nil
}
