package previewapi

import (
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficapi"
)

func normalizeRequest(spec *Spec) *controlplaneapi.Error {
	spec.Name = strings.TrimSpace(spec.Name)
	if len(validation.IsDNS1123Label(spec.Name)) != 0 {
		return controlplaneapi.Invalid("name", "Service name is invalid")
	}
	if len(spec.Ports) == 0 || len(spec.Ports) > 64 {
		return controlplaneapi.Invalid("ports", "one to 64 Service ports are required")
	}
	seenPorts := make(map[string]struct{}, len(spec.Ports))
	seenNames := make(map[string]struct{}, len(spec.Ports))
	for index := range spec.Ports {
		port := &spec.Ports[index]
		port.Name = strings.TrimSpace(port.Name)
		port.Protocol = strings.ToLower(strings.TrimSpace(port.Protocol))
		if port.ServicePort < 1 || port.ServicePort > 65535 ||
			(port.Protocol != "tcp" && port.Protocol != "udp") {
			return controlplaneapi.Invalid("ports", "Service port and protocol are invalid")
		}
		if port.Name == "" {
			port.Name = fmt.Sprintf("%s-%d", port.Protocol, port.ServicePort)
		}
		if len(validation.IsDNS1123Label(port.Name)) != 0 {
			return controlplaneapi.Invalid("ports", "Service port name is invalid")
		}
		key := fmt.Sprintf("%s/%d", port.Protocol, port.ServicePort)
		if _, exists := seenPorts[key]; exists {
			return controlplaneapi.Invalid("ports", "Service ports must be unique")
		}
		if _, exists := seenNames[port.Name]; exists {
			return controlplaneapi.Invalid("ports", "Service port names must be unique")
		}
		seenPorts[key], seenNames[port.Name] = struct{}{}, struct{}{}
	}
	slices.SortFunc(spec.Ports, trafficapi.ComparePorts)
	if apiError := trafficapi.NormalizeLocalTargets(&spec.LocalTargets, spec.Ports); apiError != nil {
		return apiError
	}
	return nil
}

var apiErrors = task.Errors()
