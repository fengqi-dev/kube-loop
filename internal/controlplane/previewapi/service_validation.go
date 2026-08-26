package previewapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/labstack/echo/v5"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/entity"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
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
	return nil
}

func comparePorts(left, right entity.Port) int {
	if left.ServicePort != right.ServicePort {
		return int(left.ServicePort - right.ServicePort)
	}
	return strings.Compare(left.Protocol, right.Protocol)
}

func decodeTask(task storage.Task, namespace string) (Document, error) {
	var spec storedSpec
	if task.Type != TaskType || json.Unmarshal(task.Spec, &spec) != nil ||
		spec.Name == "" ||
		len(spec.Ports) == 0 {
		return Document{}, errors.New("stored Preview Task is invalid")
	}
	return documentFrom(task, namespace, spec), nil
}

func documentFrom(
	task storage.Task,
	namespace string,
	spec storedSpec,
) Document {
	result := ownerResult{}
	_ = json.Unmarshal(task.Result, &result)
	return Document{
		ID: task.ID, SessionID: task.SessionID, Namespace: namespace, State: task.State,
		Name: spec.Name, ClusterIP: result.ClusterIP, Ports: append([]entity.Port(nil), spec.Ports...),
		CreatedAt: task.CreatedAt.UTC(), UpdatedAt: task.UpdatedAt.UTC(),
	}
}

func owned(
	task storage.Task,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) bool {
	return task.Type == TaskType && task.IdentityID == identity.Subject &&
		task.SessionID == session.ID
}

func storageError(err error) *controlplaneapi.Error {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return notFound()
	case errors.Is(err, storage.ErrConflict),
		errors.Is(err, storage.ErrIdempotencyMismatch):
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
