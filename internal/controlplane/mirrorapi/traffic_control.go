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
