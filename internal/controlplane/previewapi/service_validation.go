package previewapi

import (
	"fmt"
	"slices"
	"strings"

	"github.com/labstack/echo/v5"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/servicemodel"
)

func normalizeRequest(spec *Spec) *controlplaneapi.Error {
	spec.Name = strings.TrimSpace(spec.Name)
	if len(validation.IsDNS1123Label(spec.Name)) != 0 {
		return invalid("name", "Service name is invalid")
	}
	if len(spec.Ports) == 0 || len(spec.Ports) > 64 {
		return invalid("ports", "one to 64 Service ports are required")
	}
	seenPorts := make(map[string]struct{}, len(spec.Ports))
	seenNames := make(map[string]struct{}, len(spec.Ports))
	for index := range spec.Ports {
		port := &spec.Ports[index]
		port.Name = strings.TrimSpace(port.Name)
		port.Protocol = strings.ToLower(strings.TrimSpace(port.Protocol))
		if port.ServicePort < 1 || port.ServicePort > 65535 ||
			(port.Protocol != "tcp" && port.Protocol != "udp") {
			return invalid("ports", "Service port and protocol are invalid")
		}
		if port.Name == "" {
			port.Name = fmt.Sprintf("%s-%d", port.Protocol, port.ServicePort)
		}
		if len(validation.IsDNS1123Label(port.Name)) != 0 {
			return invalid("ports", "Service port name is invalid")
		}
		key := fmt.Sprintf("%s/%d", port.Protocol, port.ServicePort)
		if _, exists := seenPorts[key]; exists {
			return invalid("ports", "Service ports must be unique")
		}
		if _, exists := seenNames[port.Name]; exists {
			return invalid("ports", "Service port names must be unique")
		}
		seenPorts[key], seenNames[port.Name] = struct{}{}, struct{}{}
	}
	slices.SortFunc(spec.Ports, comparePorts)
	if apiError := normalizeLocalTargets(&spec.LocalTargets, spec.Ports); apiError != nil {
		return apiError
	}
	return nil
}

// The helpers below delegate to internal/controlplane/trafficapi, which owns
// what all three traffic task APIs do identically. They stay declared here so
// the call sites in this package read against its own vocabulary.

var apiErrors = trafficapi.Errors{Name: "Preview"}

func invalid(field, message string) *controlplaneapi.Error {
	return trafficapi.Invalid(field, message)
}

func notFound() *controlplaneapi.Error { return trafficapi.NotFound() }

func storageError(err error) *controlplaneapi.Error { return apiErrors.Storage(err) }

func internalError(err error) *controlplaneapi.Error { return apiErrors.Internal(err) }

func writeJSON(ctx *echo.Context, status int, value any) {
	trafficapi.WriteJSON(ctx, status, value)
}

func normalizeLocalTargets(
	targets *[]servicemodel.LocalTarget,
	ports []servicemodel.Port,
) *controlplaneapi.Error {
	return trafficapi.NormalizeLocalTargets(targets, ports)
}

func comparePorts(left, right servicemodel.Port) int { return trafficapi.ComparePorts(left, right) }
