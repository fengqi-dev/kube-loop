package previewapi

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/labstack/echo/v5"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/entity"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
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

func normalizeLocalTargets(
	targets *[]entity.LocalTarget,
	ports []entity.Port,
) *controlplaneapi.Error {
	if len(*targets) == 0 {
		return nil
	}
	if len(*targets) != len(ports) {
		return invalid("localTargets", "local targets must match Service ports")
	}
	expected := make(map[string]struct{}, len(ports))
	for _, port := range ports {
		expected[localTargetKey(port.ServicePort, port.Protocol)] = struct{}{}
	}
	seen := make(map[string]struct{}, len(*targets))
	for index := range *targets {
		target := &(*targets)[index]
		target.Protocol = strings.ToLower(strings.TrimSpace(target.Protocol))
		target.LocalHost = strings.TrimSpace(target.LocalHost)
		if target.LocalHost == "" {
			target.LocalHost = "127.0.0.1"
		}
		address := net.ParseIP(target.LocalHost)
		invalidHost := address != nil && (address.IsUnspecified() || address.IsMulticast())
		invalidHost = invalidHost || address == nil && len(validation.IsDNS1123Subdomain(target.LocalHost)) != 0
		key := localTargetKey(target.ServicePort, target.Protocol)
		_, matchesPort := expected[key]
		_, duplicate := seen[key]
		if target.ServicePort < 1 || target.ServicePort > 65535 || target.LocalPort < 1 ||
			(target.Protocol != "tcp" && target.Protocol != "udp") ||
			invalidHost || !matchesPort || duplicate {
			return invalid("localTargets", "local target is invalid")
		}
		seen[key] = struct{}{}
	}
	slices.SortFunc(*targets, compareLocalTargets)
	return nil
}

func localTargetKey(port int32, protocol string) string {
	return fmt.Sprintf("%d/%s", port, strings.ToLower(strings.TrimSpace(protocol)))
}

func compareLocalTargets(left, right entity.LocalTarget) int {
	if left.ServicePort != right.ServicePort {
		return int(left.ServicePort - right.ServicePort)
	}
	return strings.Compare(left.Protocol, right.Protocol)
}

func comparePorts(left, right entity.Port) int {
	if left.ServicePort != right.ServicePort {
		return int(left.ServicePort - right.ServicePort)
	}
	return strings.Compare(left.Protocol, right.Protocol)
}

func storageError(err error) *controlplaneapi.Error {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return notFound()
	case errors.Is(err, storage.ErrConflict),
		errors.Is(err, storage.ErrIdempotencyMismatch),
		errors.Is(err, trafficbindingclient.ErrTrafficBindingConflict):
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: "Preview Task conflicts with existing state",
			Cause:   err,
		}
	default:
		return internalError(err)
	}
}

func invalid(field, message string) *controlplaneapi.Error {
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeInvalidArgument,
		Field:   field,
		Message: message,
	}
}

func notFound() *controlplaneapi.Error {
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeNotFound,
		Message: "resource not found",
	}
}

func internalError(err error) *controlplaneapi.Error {
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeInternal,
		Message: "Preview operation failed",
		Cause:   err,
	}
}

func writeJSON(ctx *echo.Context, status int, value any) {
	_ = ctx.JSON(status, value)
}
