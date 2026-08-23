package previewapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

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
			Message: previewTaskOwnershipMessage,
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
			fmt.Errorf("stored Preview Task has invalid state %q", task.State),
		)
	default:
		return trafficcontrol.HeartbeatResponse{}, internalError(
			fmt.Errorf("stored Preview Task has invalid state %q", task.State),
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
			Message: previewTaskOwnershipMessage,
		}
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
		deleteContext, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			handler.config.DeleteTimeout,
		)
		deleteErr = handler.resources.Delete(
			deleteContext,
			servicebinding.PreviewServiceSnapshot{Namespace: session.Namespace},
			task.ID,
		)
		cancel()
		deleted = deleteErr == nil
	}
	var cause error
	if request.Failed {
		cause = errors.New(strings.TrimSpace(request.Reason))
		if cause.Error() == "" {
			cause = errors.New("preview relay failed")
		}
	}
	handler.finishPreview(
		task.ID,
		cleanupRequired,
		deleted,
		errors.Join(cause, deleteErr),
	)
	current, err := handler.storage.Tasks().GetByID(ctx, task.ID)
	if err != nil {
		return trafficcontrol.FinishResponse{}, storageError(err)
	}
	return trafficcontrol.FinishResponse{State: string(current.State)}, nil
}
