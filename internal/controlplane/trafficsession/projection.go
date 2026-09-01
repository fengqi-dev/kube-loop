package trafficsession

import (
	"strings"
	"time"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/entity"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

func State(binding *trafficv1alpha1.TrafficBinding) remotetask.State {
	if binding == nil || !binding.DeletionTimestamp.IsZero() {
		return remotetask.Deleted
	}
	if strings.TrimSpace(binding.Status.RelayError) != "" {
		return remotetask.Failed
	}
	switch binding.Status.Phase {
	case "", trafficv1alpha1.TrafficBindingPhasePending:
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
		return remotetask.Pending
	}
}

func Owned(
	binding *trafficv1alpha1.TrafficBinding,
	identityID, sessionID, namespace string,
) bool {
	return binding != nil && binding.Spec.IdentityID == identityID &&
		binding.Spec.SessionID == sessionID && binding.Namespace == namespace
}

func Ports(binding *trafficv1alpha1.TrafficBinding) []entity.Port {
	ports := make([]entity.Port, 0, len(binding.Spec.Ports))
	for _, port := range binding.Spec.Ports {
		ports = append(ports, entity.Port{
			Name: port.Name, ServicePort: port.TargetPort,
			Protocol: strings.ToLower(string(port.Protocol)),
		})
	}
	return ports
}

func LocalTargets(binding *trafficv1alpha1.TrafficBinding) []entity.LocalTarget {
	targets := make([]entity.LocalTarget, 0, len(binding.Spec.Ports))
	for _, port := range binding.Spec.Ports {
		if port.LocalPort == nil {
			continue
		}
		targets = append(targets, entity.LocalTarget{
			Protocol: strings.ToLower(string(port.Protocol)), ServicePort: port.TargetPort,
			// CRD validation restricts localPort to the uint16 range.
			LocalHost: port.LocalHost,
			//nolint:gosec // CRD validation restricts localPort to the uint16 range.
			LocalPort: uint16(*port.LocalPort),
		})
	}
	return targets
}

func UpdatedAt(binding *trafficv1alpha1.TrafficBinding) time.Time {
	updatedAt := binding.CreationTimestamp.Time
	for _, condition := range binding.Status.Conditions {
		if condition.LastTransitionTime.Time.After(updatedAt) {
			updatedAt = condition.LastTransitionTime.Time
		}
	}
	if binding.Status.RelayHeartbeatAt != nil && binding.Status.RelayHeartbeatAt.Time.After(updatedAt) {
		updatedAt = binding.Status.RelayHeartbeatAt.Time
	}
	return updatedAt
}
