package exchangeapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
)

func normalizeRequest(spec *Spec) *controlplaneapi.Error {
	spec.Service = strings.TrimSpace(spec.Service)
	if len(validation.IsDNS1123Subdomain(spec.Service)) != 0 {
		return invalid("service", "Service name is invalid")
	}
	if len(spec.Ports) == 0 || len(spec.Ports) > 64 {
		return invalid("ports", "one to 64 Service ports are required")
	}
	seen := make(map[string]struct{}, len(spec.Ports))
	for index := range spec.Ports {
		port := &spec.Ports[index]
		port.Name = strings.TrimSpace(port.Name)
		port.Protocol = strings.ToLower(strings.TrimSpace(port.Protocol))
		if port.ServicePort < 1 || port.ServicePort > 65535 ||
			(port.Protocol != "tcp" && port.Protocol != "udp") {
			return invalid("ports", "Service port and protocol are invalid")
		}
		key := fmt.Sprintf("%s/%d", port.Protocol, port.ServicePort)
		if _, exists := seen[key]; exists {
			return invalid("ports", "Service ports must be unique")
		}
		seen[key] = struct{}{}
	}
	slices.SortFunc(spec.Ports, comparePorts)
	return nil
}

func comparePorts(left, right trafficmodel.Port) int {
	if left.ServicePort != right.ServicePort {
		return int(left.ServicePort - right.ServicePort)
	}
	return strings.Compare(left.Protocol, right.Protocol)
}

func decodeTask(task storage.Task, namespace string) (Document, error) {
	var spec storedSpec
	if task.Type != TaskType || json.Unmarshal(task.Spec, &spec) != nil ||
		spec.Service == "" ||
		spec.ClusterIP == "" ||
		len(spec.Ports) == 0 {
		return Document{}, errors.New("stored Exchange Task is invalid")
	}
	return documentFrom(task, namespace, spec), nil
}

func documentFrom(
	task storage.Task,
	namespace string,
	spec storedSpec,
) Document {
	expiresAt := time.Time{}
	if task.ExpiresAt != nil {
		expiresAt = task.ExpiresAt.UTC()
	}
	return Document{
		ID: task.ID, SessionID: task.SessionID, Namespace: namespace, State: task.State,
		Service: spec.Service, ClusterIP: spec.ClusterIP, Ports: append([]trafficmodel.Port(nil), spec.Ports...),
		CreatedAt: task.CreatedAt.UTC(), UpdatedAt: task.UpdatedAt.UTC(), ExpiresAt: expiresAt,
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

func targetError(err error) *controlplaneapi.Error {
	switch {
	case apierrors.IsForbidden(err):
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeForbidden,
			Message: "Kubernetes Exchange access is not permitted",
			Cause:   err,
		}
	case apierrors.IsNotFound(err):
		return notFound()
	default:
		return invalid("service", err.Error())
	}
}

func storageError(err error) *controlplaneapi.Error {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return notFound()
	case errors.Is(err, storage.ErrConflict),
		errors.Is(err, storage.ErrIdempotencyMismatch):
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: "Exchange Task conflicts with existing state",
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
		Message: "Exchange operation failed",
		Cause:   err,
	}
}

func writeJSON(ctx *echo.Context, status int, value any) {
	_ = ctx.JSON(status, value)
}
