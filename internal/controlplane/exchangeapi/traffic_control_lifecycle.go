package exchangeapi

import (
	"context"
	"errors"
	"strings"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

func (handler *Service) Heartbeat(
	ctx context.Context,
	relayID string,
	request trafficcontrol.HeartbeatRequest,
) (trafficcontrol.HeartbeatResponse, *controlplaneapi.Error) {
	sessions, err := handler.bindingSessions()
	if err != nil {
		return trafficcontrol.HeartbeatResponse{}, internalError(err)
	}
	binding, err := sessions.FindSession(ctx, request.TaskID)
	if err != nil || binding.Spec.Mode != trafficv1alpha1.TrafficBindingModeExchange {
		return trafficcontrol.HeartbeatResponse{}, notFound()
	}
	if binding.Status.RelayOwnerID != relayID {
		return trafficcontrol.HeartbeatResponse{}, &controlplaneapi.Error{
			Code: controlplaneapi.CodeConflict, Message: exchangeTaskOwnershipMessage,
		}
	}
	if binding.Spec.DesiredState == trafficv1alpha1.TrafficBindingDesiredStatePaused ||
		!binding.DeletionTimestamp.IsZero() {
		return trafficcontrol.HeartbeatResponse{Stop: true}, nil
	}
	if err := sessions.RelayHeartbeat(ctx, binding, relayID); err != nil {
		return trafficcontrol.HeartbeatResponse{}, internalError(err)
	}
	return trafficcontrol.HeartbeatResponse{}, nil
}

func (handler *Service) Finish(
	ctx context.Context,
	relayID string,
	request trafficcontrol.FinishRequest,
) (trafficcontrol.FinishResponse, *controlplaneapi.Error) {
	sessions, err := handler.bindingSessions()
	if err != nil {
		return trafficcontrol.FinishResponse{}, internalError(err)
	}
	binding, err := sessions.FindSession(ctx, request.TaskID)
	if err != nil || binding.Spec.Mode != trafficv1alpha1.TrafficBindingModeExchange {
		return trafficcontrol.FinishResponse{}, notFound()
	}
	if binding.Status.RelayOwnerID != relayID {
		return trafficcontrol.FinishResponse{}, &controlplaneapi.Error{
			Code: controlplaneapi.CodeConflict, Message: exchangeTaskOwnershipMessage,
		}
	}
	reason := ""
	if request.Failed {
		reason = strings.TrimSpace(request.Reason)
		if reason == "" {
			reason = "exchange relay failed"
		}
	}
	restoreContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), handler.config.RestoreTimeout,
	)
	restoreErr := handler.resources.Restore(
		restoreContext, serviceSnapshot(binding.Namespace), binding.Spec.TaskID,
	)
	cancel()
	if restoreErr != nil {
		reason = errors.Join(errors.New(reason), restoreErr).Error()
	}
	if current, reloadErr := sessions.FindSession(ctx, request.TaskID); reloadErr == nil {
		binding = current
	} else if restoreErr == nil {
		return trafficcontrol.FinishResponse{}, internalError(reloadErr)
	}
	if err := sessions.FinishRelay(ctx, binding, relayID, reason); err != nil {
		return trafficcontrol.FinishResponse{}, internalError(err)
	}
	state := remotetask.Stopped
	if request.Failed || restoreErr != nil {
		state = remotetask.Failed
	}
	return trafficcontrol.FinishResponse{State: string(state)}, nil
}
