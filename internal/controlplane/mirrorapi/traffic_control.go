package mirrorapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
)

type ownerResult struct {
	OwnerID       string `json:"ownerId"`
	GatewayIP     string `json:"gatewayIp"`
	Restored      bool   `json:"restored,omitempty"`
	StopRequested bool   `json:"stopRequested,omitempty"`
	Error         string `json:"error,omitempty"`
}

func (handler *Service) Heartbeat(
	ctx context.Context,
	relayID string,
	request trafficcontrol.HeartbeatRequest,
) (trafficcontrol.HeartbeatResponse, *controlplaneapi.Error) {
	task, err := handler.storage.Tasks().GetByID(ctx, request.TaskID)
	if err != nil || task.Type != TaskType {
		return trafficcontrol.HeartbeatResponse{}, notFound()
	}
	if !trafficOwnedBy(task, relayID) {
		return trafficcontrol.HeartbeatResponse{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: mirrorTaskOwnershipMessage,
		}
	}
	switch task.State {
	case remotetask.Starting, remotetask.Running:
		if err := handler.updateTrafficTask(
			ctx, task.ID, task.State, task.State, task.Result,
		); err != nil &&
			!errors.Is(err, storage.ErrConflict) {
			return trafficcontrol.HeartbeatResponse{}, storageError(err)
		}
		return trafficcontrol.HeartbeatResponse{}, nil
	case remotetask.Stopping,
		remotetask.Stopped,
		remotetask.Failed,
		remotetask.Recovering:
		return trafficcontrol.HeartbeatResponse{Stop: true}, nil
	case remotetask.Pending:
		return trafficcontrol.HeartbeatResponse{}, internalError(
			fmt.Errorf("stored Mirror Task has invalid state %q", task.State),
		)
	default:
		return trafficcontrol.HeartbeatResponse{}, internalError(
			fmt.Errorf("stored Mirror Task has invalid state %q", task.State),
		)
	}
}

func (handler *Service) Finish(
	ctx context.Context,
	relayID string,
	request trafficcontrol.FinishRequest,
) (trafficcontrol.FinishResponse, *controlplaneapi.Error) {
	task, err := handler.storage.Tasks().GetByID(ctx, request.TaskID)
	if err != nil || task.Type != TaskType {
		return trafficcontrol.FinishResponse{}, notFound()
	}
	if !trafficOwnedBy(task, relayID) {
		return trafficcontrol.FinishResponse{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: mirrorTaskOwnershipMessage,
		}
	}
	session, err := handler.storage.Sessions().GetByID(ctx, task.SessionID)
	if err != nil {
		return trafficcontrol.FinishResponse{}, storageError(err)
	}
	var owner ownerResult
	_ = json.Unmarshal(task.Result, &owner)
	cleanupRequired := owner.GatewayIP != ""
	restored := !cleanupRequired
	var restoreErr error
	if cleanupRequired {
		restoreContext, cancel := context.WithTimeout(
			context.Background(),
			handler.config.RestoreTimeout,
		)
		restoreErr = handler.resources.Restore(
			restoreContext,
			servicebinding.ServiceInterceptSnapshot{
				Namespace: session.Namespace,
			},
			task.ID,
		)
		cancel()
		restored = restoreErr == nil
	}
	var cause error
	if request.Failed {
		cause = errors.New(strings.TrimSpace(request.Reason))
		if cause.Error() == "" {
			cause = errors.New("mirror relay failed")
		}
	}
	handler.finishMirror(
		task.ID,
		cleanupRequired,
		restored,
		errors.Join(cause, restoreErr),
	)
	current, err := handler.storage.Tasks().GetByID(ctx, task.ID)
	if err != nil {
		return trafficcontrol.FinishResponse{}, storageError(err)
	}
	return trafficcontrol.FinishResponse{State: string(current.State)}, nil
}

func (handler *Service) trafficSession(
	ctx context.Context,
	ticketIdentity trafficcontrol.Identity,
) (controlplaneapi.Identity, sessionapi.ActiveSession, *controlplaneapi.Error) {
	identity := controlplaneapi.Identity{
		Subject:  ticketIdentity.IdentityID,
		Groups:   append([]string(nil), ticketIdentity.Groups...),
		DeviceID: ticketIdentity.DeviceID,
	}
	session, apiError := handler.sessions.RequireActive(
		ctx,
		identity,
		ticketIdentity.Namespace,
		ticketIdentity.SessionID,
	)
	if apiError != nil {
		return controlplaneapi.Identity{}, sessionapi.ActiveSession{}, apiError
	}
	if !sessionapi.AcceptsStreamGeneration(
		session.Generation,
		ticketIdentity.SessionGeneration,
	) {
		return controlplaneapi.Identity{}, sessionapi.ActiveSession{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: "Session generation changed",
		}
	}
	return identity, session, nil
}

func (handler *Service) updateTrafficTask(
	ctx context.Context,
	taskID string,
	from remotetask.State,
	to remotetask.State,
	result json.RawMessage,
) error {
	return handler.storage.Tasks().UpdateState(ctx, taskID, from, to, result, handler.now().UTC())
}

func trafficOwnedBy(task storage.Task, relayID string) bool {
	var owner ownerResult
	return json.Unmarshal(task.Result, &owner) == nil &&
		owner.OwnerID == relayID
}

func interceptPorts(
	expected []trafficmodel.Port,
	listeners []trafficcontrol.ListenerPort,
) ([]servicebinding.InterceptPort, error) {
	if len(expected) != len(listeners) {
		return nil, errors.New(
			"gateway listener ports do not match the Mirror Task",
		)
	}
	byKey := make(map[string]trafficcontrol.ListenerPort, len(listeners))
	for _, port := range listeners {
		byKey[strings.ToLower(port.Protocol)+fmt.Sprintf("/%d", port.ServicePort)] = port
	}
	result := make([]servicebinding.InterceptPort, 0, len(expected))
	for _, port := range expected {
		listener, ok := byKey[strings.ToLower(port.Protocol)+fmt.Sprintf("/%d", port.ServicePort)]
		if !ok || listener.Name != port.Name {
			return nil, errors.New(
				"gateway listener ports do not match the Mirror Task",
			)
		}
		result = append(result, servicebinding.InterceptPort{
			Name: port.Name, Protocol: corev1.Protocol(strings.ToUpper(port.Protocol)),
			ServicePort: port.ServicePort, ListenPort: listener.ListenPort,
		})
	}
	return result, nil
}

func (handler *Service) finishMirror(
	taskID string,
	cleanupRequired, restored bool,
	cause error,
) bool {
	return taskstream.Finish(taskstream.FinishConfig{
		Tasks: handler.storage.Tasks(), TaskID: taskID, Now: handler.now, Cause: cause,
		CleanupRequired: cleanupRequired, CleanupComplete: restored,
		Result: func(task storage.Task, next remotetask.State, cleanupPending bool) json.RawMessage {
			var result ownerResult
			_ = json.Unmarshal(task.Result, &result)
			result.Restored = restored
			if next == remotetask.Failed {
				result.Error = "Mirror stream failed"
			}
			if cleanupPending {
				result.Error = "Mirror resource restoration is pending"
			}
			encoded, _ := json.Marshal(result)
			return encoded
		},
	})
}
