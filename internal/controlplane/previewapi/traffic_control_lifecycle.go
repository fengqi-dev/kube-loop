package previewapi

import (
	"context"
	"errors"
	"strings"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

func (handler *Service) Heartbeat(ctx context.Context, relayID string,
	request trafficcontrol.HeartbeatRequest) (trafficcontrol.HeartbeatResponse, *controlplaneapi.Error) {
	sessions, err := handler.bindingSessions()
	if err != nil {
		return trafficcontrol.HeartbeatResponse{}, internalError(err)
	}
	binding, err := sessions.FindSession(ctx, request.TaskID)
	if err != nil || binding.Spec.Mode != trafficv1alpha1.TrafficBindingModePreview {
		return trafficcontrol.HeartbeatResponse{}, notFound()
	}
	if binding.Status.RelayOwnerID != relayID {
		return trafficcontrol.HeartbeatResponse{}, &controlplaneapi.Error{
			Code: controlplaneapi.CodeConflict, Message: previewTaskOwnershipMessage,
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

func (handler *Service) Finish(ctx context.Context, relayID string,
	request trafficcontrol.FinishRequest) (trafficcontrol.FinishResponse, *controlplaneapi.Error) {
	sessions, err := handler.bindingSessions()
	if err != nil {
		return trafficcontrol.FinishResponse{}, internalError(err)
	}
	binding, err := sessions.FindSession(ctx, request.TaskID)
	if err != nil || binding.Spec.Mode != trafficv1alpha1.TrafficBindingModePreview {
		return trafficcontrol.FinishResponse{}, notFound()
	}
	if binding.Status.RelayOwnerID != relayID {
		return trafficcontrol.FinishResponse{}, &controlplaneapi.Error{
			Code: controlplaneapi.CodeConflict, Message: previewTaskOwnershipMessage,
		}
	}
	reason := ""
	if request.Failed {
		reason = strings.TrimSpace(request.Reason)
		if reason == "" {
			reason = "preview relay failed"
		}
	}
	deleteContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), handler.config.DeleteTimeout)
	deleteErr := handler.resources.Delete(deleteContext,
		servicebinding.PreviewServiceSnapshot{Namespace: binding.Namespace}, binding.Spec.TaskID)
	cancel()
	if deleteErr != nil {
		reason = errors.Join(errors.New(reason), deleteErr).Error()
	}
	if current, reloadErr := sessions.FindSession(ctx, request.TaskID); reloadErr == nil {
		binding = current
	} else if deleteErr == nil {
		return trafficcontrol.FinishResponse{}, internalError(reloadErr)
	}
	if err := sessions.FinishRelay(ctx, binding, relayID, reason); err != nil {
		return trafficcontrol.FinishResponse{}, internalError(err)
	}
	state := remotetask.Stopped
	if request.Failed || deleteErr != nil {
		state = remotetask.Failed
	}
	return trafficcontrol.FinishResponse{State: string(state)}, nil
}
