package service

import (
	"errors"
	"net"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
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

func ownedBinding(
	binding *trafficv1alpha1.TrafficBinding,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) bool {
	return binding != nil && binding.Spec.IdentityID == identity.Subject &&
		binding.Spec.SessionID == session.ID && binding.Namespace == session.Namespace
}

func isPortForward(binding *trafficv1alpha1.TrafficBinding) bool {
	return binding != nil && binding.Spec.Mode == trafficv1alpha1.TrafficBindingModePortForward &&
		binding.Spec.Target != nil && len(binding.Spec.Ports) == 1
}

func portForwardFromBinding(
	binding *trafficv1alpha1.TrafficBinding,
	session sessionapi.ActiveSession,
) PortForward {
	port := binding.Spec.Ports[0]
	kind := strings.ToLower(string(binding.Spec.Target.Kind))
	localPort := uint16(0)
	if port.LocalPort != nil {
		localPort = uint16(*port.LocalPort) //nolint:gosec // CRD validation bounds this port.
	}
	updatedAt := binding.CreationTimestamp.Time
	for _, condition := range binding.Status.Conditions {
		if condition.LastTransitionTime.Time.After(updatedAt) {
			updatedAt = condition.LastTransitionTime.Time
		}
	}
	return PortForward{
		ID: binding.Spec.TaskID, SessionID: binding.Spec.SessionID,
		Namespace: binding.Namespace, State: stateFromBinding(binding),
		Kind: kind, Name: binding.Spec.Target.Name,
		Protocol:   strings.ToLower(string(port.Protocol)),
		RemotePort: uint16(port.TargetPort), //nolint:gosec // CRD validation bounds this port.
		LocalPort:  localPort, DialAddress: binding.Spec.DialAddress,
		CreatedAt: binding.CreationTimestamp.Time, UpdatedAt: updatedAt,
		ExpiresAt: session.ExpiresAt.UTC(),
	}
}

func stateFromBinding(binding *trafficv1alpha1.TrafficBinding) remotetask.State {
	if binding == nil || !binding.DeletionTimestamp.IsZero() {
		return remotetask.Deleted
	}
	switch binding.Status.Phase {
	case trafficv1alpha1.TrafficBindingPhasePending, "":
		return remotetask.Pending
	case trafficv1alpha1.TrafficBindingPhaseReconciling:
		return remotetask.Starting
	case trafficv1alpha1.TrafficBindingPhaseReady:
		return remotetask.Running
	case trafficv1alpha1.TrafficBindingPhasePausing,
		trafficv1alpha1.TrafficBindingPhaseRestoring:
		return remotetask.Stopping
	case trafficv1alpha1.TrafficBindingPhasePaused,
		trafficv1alpha1.TrafficBindingPhaseRestored:
		return remotetask.Stopped
	case trafficv1alpha1.TrafficBindingPhaseDegraded:
		return remotetask.Failed
	default:
		return remotetask.Failed
	}
}

func mapStorageError(err error) *controlplaneapi.Error {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return notFound()
	case errors.Is(err, storage.ErrConflict),
		errors.Is(err, trafficbindingclient.ErrTrafficBindingConflict):
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
