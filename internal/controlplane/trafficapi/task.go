package trafficapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficsession"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/protocol/servicemodel"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

// Task identifies one traffic task type to the shared handlers below. Every
// message a client can see is derived from Name, so the three APIs stay
// distinguishable to a caller even though they share one implementation.
type Task struct {
	// Name is the user-facing noun: "Exchange", "Mirror" or "Preview".
	Name string
	// Mode is the TrafficBinding mode this API owns. A binding in any other
	// mode is invisible to it.
	Mode trafficv1alpha1.TrafficBindingMode
	// ClaimMode is the same task type on the traffic-control wire.
	ClaimMode trafficcontrol.Mode
}

// Errors builds the API errors for this task type.
func (task Task) Errors() Errors { return Errors{Name: task.Name} }

// OwnershipMessage rejects a relay acting for a task it does not own.
func (task Task) OwnershipMessage() string {
	return task.Name + " Task is not owned by this Gateway"
}

func (task Task) conflict(message string) *controlplaneapi.Error {
	return &controlplaneapi.Error{Code: controlplaneapi.CodeConflict, Message: message}
}

// Relay is the traffic-control half of one traffic task API: everything the
// Gateway calls that the three task types implement identically. A task API
// embeds it, so Claim, Heartbeat and Finish need no code of its own; Prepare
// differs per task and stays in the API package, built on PrepareBinding.
type Relay struct {
	Task Task
	// Sessions authorizes the identity carried by the relay's ticket.
	Sessions SessionValidator
	// Bindings opens the TrafficBinding session store. It is a function
	// because the store lives behind the API's resource dependency, which is
	// only wired once the service itself exists.
	Bindings func() (*trafficbindingclient.Manager, error)
	// ServiceName reads the task's Service out of its binding.
	ServiceName func(*trafficv1alpha1.TrafficBinding) string
	// Release gives back the Kubernetes resources the task owned.
	Release Release
	// ReleaseTimeout bounds Release, which outlives the caller's context.
	ReleaseTimeout time.Duration
}

func (relay Relay) bindings() (*trafficbindingclient.Manager, *controlplaneapi.Error) {
	bindings, err := relay.Bindings()
	if err != nil {
		return nil, relay.Task.Errors().Internal(err)
	}
	return bindings, nil
}

// Owns reports whether binding is this task type and belongs to the identity's
// active session.
func (task Task) Owns(
	binding *trafficv1alpha1.TrafficBinding,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) bool {
	return trafficsession.Owned(
		binding, identity.Subject, session.ID, session.Namespace,
	) && binding.Spec.Mode == task.Mode
}

// OwnedBinding loads the task's binding, reporting not-found for anything the
// caller may not learn about: a malformed ID, a missing binding, another
// session's binding, or another task type's binding.
func (relay Relay) OwnedBinding(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) (*trafficv1alpha1.TrafficBinding, *controlplaneapi.Error) {
	bindings, apiError := relay.bindings()
	if apiError != nil {
		return nil, apiError
	}
	if _, err := uuid.Parse(taskID); err != nil {
		return nil, controlplaneapi.NotFound()
	}
	binding, err := bindings.GetSession(ctx, session.Namespace, taskID)
	if err != nil || !relay.Task.Owns(binding, identity, session) {
		if err != nil && !errors.Is(err, trafficbindingclient.ErrTrafficBindingNotFound) {
			return nil, relay.Task.Errors().Internal(err)
		}
		return nil, controlplaneapi.NotFound()
	}
	return binding, nil
}

// relayBinding loads the binding a relay names, checking that the relay still
// owns it. Heartbeat and Finish arrive on the relay's own transport, so they
// authorize on relay ownership rather than on the client identity.
func (relay Relay) relayBinding(
	ctx context.Context,
	bindings *trafficbindingclient.Manager,
	relayID, taskID string,
) (*trafficv1alpha1.TrafficBinding, *controlplaneapi.Error) {
	binding, err := bindings.FindSession(ctx, taskID)
	if err != nil || binding.Spec.Mode != relay.Task.Mode {
		return nil, controlplaneapi.NotFound()
	}
	if binding.Status.RelayOwnerID != relayID {
		return nil, relay.Task.conflict(relay.Task.OwnershipMessage())
	}
	return binding, nil
}

// ServiceNameFromTarget reads the intercepted Service name. Exchange and
// Mirror bind an existing Service.
func ServiceNameFromTarget(binding *trafficv1alpha1.TrafficBinding) string {
	if binding.Spec.Target == nil {
		return ""
	}
	return binding.Spec.Target.Name
}

// ServiceNameFromPreview reads the created Service name. Preview owns the
// Service it publishes.
func ServiceNameFromPreview(binding *trafficv1alpha1.TrafficBinding) string {
	if binding.Spec.Preview == nil {
		return ""
	}
	return binding.Spec.Preview.ServiceName
}

// Claim hands one unclaimed task to the relay that asked for it.
func (relay Relay) Claim(
	ctx context.Context,
	relayID string,
	request trafficcontrol.ClaimRequest,
) (trafficcontrol.ClaimResponse, *controlplaneapi.Error) {
	bindings, apiError := relay.bindings()
	if apiError != nil {
		return trafficcontrol.ClaimResponse{}, apiError
	}
	identity, session, apiError := TrafficSession(ctx, relay.Sessions, request.Identity)
	if apiError != nil {
		return trafficcontrol.ClaimResponse{}, apiError
	}
	binding, apiError := relay.OwnedBinding(ctx, identity, session, request.TaskID)
	if apiError != nil {
		return trafficcontrol.ClaimResponse{}, apiError
	}
	if binding.Spec.DesiredState != trafficv1alpha1.TrafficBindingDesiredStateActive ||
		binding.Spec.Relay != nil || binding.Status.RelayOwnerID != "" {
		return trafficcontrol.ClaimResponse{}, relay.Task.conflict(
			relay.Task.Name + " Session was already claimed",
		)
	}
	if _, err := bindings.ClaimRelay(ctx, binding, relayID); err != nil {
		return trafficcontrol.ClaimResponse{}, relay.Task.Errors().Storage(err)
	}
	return trafficcontrol.ClaimResponse{
		Mode: relay.Task.ClaimMode, TaskID: binding.Spec.TaskID,
		Service: relay.ServiceName(binding),
		Ports:   append([]servicemodel.Port(nil), trafficsession.Ports(binding)...),
	}, nil
}

// PrepareBinding resolves and authorizes a Prepare request, returning the
// binding the relay may now configure. What each task does with that binding
// differs, so the caller finishes the handshake itself.
func (relay Relay) PrepareBinding(
	ctx context.Context,
	relayID string,
	request trafficcontrol.PrepareRequest,
) (
	controlplaneapi.Identity,
	sessionapi.ActiveSession,
	*trafficv1alpha1.TrafficBinding,
	*controlplaneapi.Error,
) {
	identity, session, apiError := TrafficSession(ctx, relay.Sessions, request.Identity)
	if apiError != nil {
		return controlplaneapi.Identity{}, sessionapi.ActiveSession{}, nil, apiError
	}
	binding, apiError := relay.OwnedBinding(ctx, identity, session, request.TaskID)
	if apiError != nil {
		return controlplaneapi.Identity{}, sessionapi.ActiveSession{}, nil, apiError
	}
	if binding.Status.RelayOwnerID != relayID || binding.Spec.Relay != nil {
		return controlplaneapi.Identity{}, sessionapi.ActiveSession{}, nil,
			relay.Task.conflict(relay.Task.OwnershipMessage())
	}
	return identity, session, binding, nil
}

// ListenerPorts keys the Gateway's assigned listener ports the way the
// TrafficBinding relay record stores them.
func ListenerPorts(ports []trafficcontrol.ListenerPort) map[string]int32 {
	assigned := make(map[string]int32, len(ports))
	for _, port := range ports {
		assigned[strings.ToUpper(port.Protocol)+fmt.Sprintf("/%d", port.ServicePort)] = port.ListenPort
	}
	return assigned
}

// Heartbeat keeps the relay's claim alive, or tells it to stop once the task
// has been paused or deleted underneath it.
func (relay Relay) Heartbeat(
	ctx context.Context,
	relayID string,
	request trafficcontrol.HeartbeatRequest,
) (trafficcontrol.HeartbeatResponse, *controlplaneapi.Error) {
	bindings, apiError := relay.bindings()
	if apiError != nil {
		return trafficcontrol.HeartbeatResponse{}, apiError
	}
	binding, apiError := relay.relayBinding(ctx, bindings, relayID, request.TaskID)
	if apiError != nil {
		return trafficcontrol.HeartbeatResponse{}, apiError
	}
	if binding.Spec.DesiredState == trafficv1alpha1.TrafficBindingDesiredStatePaused ||
		!binding.DeletionTimestamp.IsZero() {
		return trafficcontrol.HeartbeatResponse{Stop: true}, nil
	}
	if err := bindings.RelayHeartbeat(ctx, binding, relayID); err != nil {
		return trafficcontrol.HeartbeatResponse{}, relay.Task.Errors().Internal(err)
	}
	return trafficcontrol.HeartbeatResponse{}, nil
}

// Release gives back the Kubernetes resources a task owned. Exchange and
// Mirror restore the intercepted Service; Preview deletes the one it created.
type Release func(ctx context.Context, namespace, taskID string) error

// Finish closes out a relay's claim. Releasing the resources is best-effort
// and outlives the caller's context, because the relay is already gone by the
// time this runs; a release failure downgrades the reported state to failed
// rather than leaving the task claimed.
func (relay Relay) Finish(
	ctx context.Context,
	relayID string,
	request trafficcontrol.FinishRequest,
) (trafficcontrol.FinishResponse, *controlplaneapi.Error) {
	bindings, apiError := relay.bindings()
	if apiError != nil {
		return trafficcontrol.FinishResponse{}, apiError
	}
	binding, apiError := relay.relayBinding(ctx, bindings, relayID, request.TaskID)
	if apiError != nil {
		return trafficcontrol.FinishResponse{}, apiError
	}
	reason := ""
	if request.Failed {
		reason = strings.TrimSpace(request.Reason)
		if reason == "" {
			reason = strings.ToLower(relay.Task.Name) + " relay failed"
		}
	}
	releaseContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), relay.ReleaseTimeout,
	)
	releaseErr := relay.Release(releaseContext, binding.Namespace, binding.Spec.TaskID)
	cancel()
	if releaseErr != nil {
		reason = errors.Join(errors.New(reason), releaseErr).Error()
	}
	if current, reloadErr := bindings.FindSession(ctx, request.TaskID); reloadErr == nil {
		binding = current
	} else if releaseErr == nil {
		return trafficcontrol.FinishResponse{}, relay.Task.Errors().Internal(reloadErr)
	}
	if err := bindings.FinishRelay(ctx, binding, relayID, reason); err != nil {
		return trafficcontrol.FinishResponse{}, relay.Task.Errors().Internal(err)
	}
	state := remotetask.Stopped
	if request.Failed || releaseErr != nil {
		state = remotetask.Failed
	}
	return trafficcontrol.FinishResponse{State: string(state)}, nil
}
