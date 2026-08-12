package previewapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/servicebinding"
	corev1 "k8s.io/api/core/v1"
)

func (handler *Service) Claim(
	ctx context.Context,
	relayID string,
	request trafficcontrol.ClaimRequest,
) (trafficcontrol.ClaimResponse, *controlplaneapi.Error) {
	principal, session, apiError := handler.trafficSession(ctx, request.Identity)
	if apiError != nil {
		return trafficcontrol.ClaimResponse{}, apiError
	}
	task, apiError := handler.ownedTask(ctx, principal, session, request.TaskID)
	if apiError != nil {
		return trafficcontrol.ClaimResponse{}, apiError
	}
	if task.State != remotetask.Pending {
		return trafficcontrol.ClaimResponse{}, &controlplaneapi.Error{Code: controlplaneapi.CodeConflict, Message: "Preview Task was already claimed"}
	}
	var spec storedSpec
	if json.Unmarshal(task.Spec, &spec) != nil || spec.Name == "" || len(spec.Ports) == 0 {
		return trafficcontrol.ClaimResponse{}, internalError(errors.New("stored Preview Task is invalid"))
	}
	result, _ := json.Marshal(ownerResult{OwnerID: relayID})
	if err := handler.storage.Tasks().UpdateState(ctx, task.ID, remotetask.Pending, remotetask.Starting, result, handler.now().UTC()); err != nil {
		return trafficcontrol.ClaimResponse{}, storageError(err)
	}
	return trafficcontrol.ClaimResponse{
		Mode: trafficcontrol.ModePreview, TaskID: task.ID, Service: spec.Name,
		Ports: append([]trafficmodel.Port(nil), spec.Ports...),
	}, nil
}

func (handler *Service) Prepare(
	ctx context.Context,
	relayID string,
	request trafficcontrol.PrepareRequest,
) (trafficcontrol.PrepareResponse, *controlplaneapi.Error) {
	principal, session, apiError := handler.trafficSession(ctx, request.Identity)
	if apiError != nil {
		return trafficcontrol.PrepareResponse{}, apiError
	}
	task, apiError := handler.ownedTask(ctx, principal, session, request.TaskID)
	if apiError != nil {
		return trafficcontrol.PrepareResponse{}, apiError
	}
	if task.State != remotetask.Starting || !trafficOwnedBy(task, relayID) {
		return trafficcontrol.PrepareResponse{}, &controlplaneapi.Error{Code: controlplaneapi.CodeConflict, Message: "Preview Task is not owned by this Gateway"}
	}
	var spec storedSpec
	if json.Unmarshal(task.Spec, &spec) != nil {
		return trafficcontrol.PrepareResponse{}, internalError(errors.New("stored Preview Task is invalid"))
	}
	ports, err := interceptPorts(spec.Ports, request.Ports)
	if err != nil {
		return trafficcontrol.PrepareResponse{}, &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Message: err.Error(), Cause: err}
	}
	claim := ownerResult{OwnerID: relayID, GatewayIP: request.GatewayIP}
	claimJSON, _ := json.Marshal(claim)
	if err := handler.storage.Tasks().UpdateState(ctx, task.ID, remotetask.Starting, remotetask.Starting, claimJSON, handler.now().UTC()); err != nil {
		return trafficcontrol.PrepareResponse{}, storageError(err)
	}
	snapshot := servicebinding.PreviewServiceSnapshot{
		Namespace: session.Namespace, Service: spec.Name, GatewayIP: request.GatewayIP, Ports: ports,
	}
	service, err := handler.resources.Create(ctx, principal, snapshot, task.ID)
	if err != nil {
		return trafficcontrol.PrepareResponse{}, internalError(fmt.Errorf("create Preview binding: %w", err))
	}
	if service == nil || service.Spec.ClusterIP == "" {
		return trafficcontrol.PrepareResponse{}, internalError(errors.New("created Preview Service has no ClusterIP"))
	}
	claim.ClusterIP = service.Spec.ClusterIP
	claimJSON, _ = json.Marshal(claim)
	if err := handler.storage.Tasks().UpdateState(ctx, task.ID, remotetask.Starting, remotetask.Running, claimJSON, handler.now().UTC()); err != nil {
		return trafficcontrol.PrepareResponse{}, storageError(err)
	}
	return trafficcontrol.PrepareResponse{ClusterIP: service.Spec.ClusterIP}, nil
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
		return trafficcontrol.HeartbeatResponse{}, &controlplaneapi.Error{Code: controlplaneapi.CodeConflict, Message: "Preview Task is not owned by this Gateway"}
	}
	switch task.State {
	case remotetask.Starting, remotetask.Running:
		if err := handler.storage.Tasks().UpdateState(ctx, task.ID, task.State, task.State, task.Result, handler.now().UTC()); err != nil && !errors.Is(err, storage.ErrConflict) {
			return trafficcontrol.HeartbeatResponse{}, storageError(err)
		}
		return trafficcontrol.HeartbeatResponse{}, nil
	case remotetask.Stopping, remotetask.Stopped, remotetask.Failed, remotetask.Recovering:
		return trafficcontrol.HeartbeatResponse{Stop: true}, nil
	default:
		return trafficcontrol.HeartbeatResponse{}, internalError(fmt.Errorf("stored Preview Task has invalid state %q", task.State))
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
		return trafficcontrol.FinishResponse{}, &controlplaneapi.Error{Code: controlplaneapi.CodeConflict, Message: "Preview Task is not owned by this Gateway"}
	}
	session, err := handler.storage.Sessions().GetByID(ctx, task.SessionID)
	if err != nil {
		return trafficcontrol.FinishResponse{}, storageError(err)
	}
	var owner ownerResult
	_ = json.Unmarshal(task.Result, &owner)
	cleanupRequired := owner.GatewayIP != ""
	deleted := !cleanupRequired
	var deleteErr error
	if cleanupRequired {
		deleteContext, cancel := context.WithTimeout(context.Background(), handler.config.DeleteTimeout)
		deleteErr = handler.resources.Delete(deleteContext, servicebinding.PreviewServiceSnapshot{Namespace: session.Namespace}, task.ID)
		cancel()
		deleted = deleteErr == nil
	}
	var cause error
	if request.Failed {
		cause = errors.New(strings.TrimSpace(request.Reason))
		if cause.Error() == "" {
			cause = errors.New("Preview relay failed")
		}
	}
	handler.finishPreview(task.ID, cleanupRequired, deleted, errors.Join(cause, deleteErr))
	current, err := handler.storage.Tasks().GetByID(ctx, task.ID)
	if err != nil {
		return trafficcontrol.FinishResponse{}, storageError(err)
	}
	return trafficcontrol.FinishResponse{State: string(current.State)}, nil
}

func (handler *Service) trafficSession(
	ctx context.Context,
	identity trafficcontrol.Identity,
) (controlplaneapi.Principal, sessionapi.ActiveSession, *controlplaneapi.Error) {
	principal := controlplaneapi.Principal{
		Subject: identity.PrincipalID, Groups: append([]string(nil), identity.Groups...), DeviceID: identity.DeviceID,
	}
	session, apiError := handler.sessions.RequireActive(ctx, principal, identity.Namespace, identity.SessionID)
	if apiError != nil {
		return controlplaneapi.Principal{}, sessionapi.ActiveSession{}, apiError
	}
	if session.Generation != identity.SessionGeneration {
		return controlplaneapi.Principal{}, sessionapi.ActiveSession{}, &controlplaneapi.Error{Code: controlplaneapi.CodeConflict, Message: "Session generation changed"}
	}
	return principal, session, nil
}

func trafficOwnedBy(task storage.Task, relayID string) bool {
	var owner ownerResult
	return json.Unmarshal(task.Result, &owner) == nil && owner.OwnerID == relayID
}

func interceptPorts(expected []trafficmodel.Port, listeners []trafficcontrol.ListenerPort) ([]servicebinding.InterceptPort, error) {
	if len(expected) != len(listeners) {
		return nil, errors.New("Gateway listener ports do not match the Preview Task")
	}
	byKey := make(map[string]trafficcontrol.ListenerPort, len(listeners))
	for _, port := range listeners {
		byKey[strings.ToLower(port.Protocol)+fmt.Sprintf("/%d", port.ServicePort)] = port
	}
	result := make([]servicebinding.InterceptPort, 0, len(expected))
	for _, port := range expected {
		listener, ok := byKey[strings.ToLower(port.Protocol)+fmt.Sprintf("/%d", port.ServicePort)]
		if !ok || listener.Name != port.Name {
			return nil, errors.New("Gateway listener ports do not match the Preview Task")
		}
		result = append(result, servicebinding.InterceptPort{
			Name: port.Name, Protocol: corev1.Protocol(strings.ToUpper(port.Protocol)),
			ServicePort: port.ServicePort, ListenPort: listener.ListenPort,
		})
	}
	return result, nil
}

func (handler *Service) finishPreview(taskID string, cleanupRequired, deleted bool, cause error) bool {
	return taskstream.Finish(taskstream.FinishConfig{
		Tasks: handler.storage.Tasks(), TaskID: taskID, Now: handler.now, Cause: cause,
		CleanupRequired: cleanupRequired, CleanupComplete: deleted,
		Result: func(task storage.Task, next remotetask.State, cleanupPending bool) json.RawMessage {
			var result ownerResult
			_ = json.Unmarshal(task.Result, &result)
			result.Deleted = deleted
			if next == remotetask.Failed {
				result.Error = "Preview stream failed"
			}
			if cleanupPending {
				result.Error = "Preview resource deletion is pending"
			}
			encoded, _ := json.Marshal(result)
			return encoded
		},
	})
}
