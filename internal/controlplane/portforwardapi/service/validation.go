package service

import (
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func normalizeSpec(spec *Spec) *controlplaneapi.Error {
	spec.Kind = strings.ToLower(strings.TrimSpace(spec.Kind))
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Protocol = strings.ToLower(strings.TrimSpace(spec.Protocol))
	if spec.Kind != "pod" && spec.Kind != "service" {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   "kind",
			Message: "kind must be pod or service",
		}
	}
	if len(validation.IsDNS1123Subdomain(spec.Name)) != 0 {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   "name",
			Message: "target name is invalid",
		}
	}
	if spec.Protocol == "" {
		spec.Protocol = "tcp"
	}
	if spec.Protocol != "tcp" && spec.Protocol != "udp" {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   "protocol",
			Message: "protocol must be tcp or udp",
		}
	}
	if spec.RemotePort == 0 {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   "remotePort",
			Message: "remotePort is required",
		}
	}
	return nil
}

func validateTarget(target Target) error {
	if strings.TrimSpace(target.Host) != target.Host || target.Host == "" ||
		target.Port == 0 ||
		net.ParseIP(target.Host) == nil {
		return errors.New("resolved target must contain an IP address and port")
	}
	return nil
}

func owned(
	task storage.Task,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) bool {
	return task.Type == TaskType && task.IdentityID == identity.Subject &&
		task.SessionID == session.ID
}

func portForwardFromTask(task storage.Task, namespace string) PortForward {
	portForward, _ := decodeTask(task, namespace)
	return portForward
}

func decodeTask(task storage.Task, namespace string) (PortForward, error) {
	var spec Spec
	var target Target
	if err := json.Unmarshal(task.Spec, &spec); err != nil {
		return PortForward{}, errors.New("decode Port Forward task spec")
	}
	if err := json.Unmarshal(task.Result, &target); err != nil {
		return PortForward{}, errors.New("decode Port Forward task target")
	}
	if apiError := normalizeSpec(&spec); apiError != nil {
		return PortForward{}, errors.New(
			"stored Port Forward task spec is invalid",
		)
	}
	if err := validateTarget(target); err != nil {
		return PortForward{}, err
	}
	expiresAt := time.Time{}
	if task.ExpiresAt != nil {
		expiresAt = task.ExpiresAt.UTC()
	}
	return PortForward{
		ID: task.ID, SessionID: task.SessionID, Namespace: namespace, State: task.State,
		Kind: spec.Kind, Name: spec.Name, Protocol: spec.Protocol, RemotePort: spec.RemotePort,
		DialAddress: target.Address(), CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt, ExpiresAt: expiresAt,
	}, nil
}

func mapStorageError(err error) *controlplaneapi.Error {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return notFound()
	case errors.Is(err, storage.ErrConflict):
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: "Port Forward Task state changed; reload and retry",
			Cause:   err,
		}
	case errors.Is(err, storage.ErrIdempotencyMismatch):
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: "Idempotency-Key was already used for a different request",
			Cause:   err,
		}
	default:
		return internalError(err)
	}
}

func internalError(err error) *controlplaneapi.Error {
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeInternal,
		Message: "Port Forward Task operation failed",
		Cause:   err,
	}
}

func notFound() *controlplaneapi.Error {
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeNotFound,
		Message: "resource not found",
	}
}
