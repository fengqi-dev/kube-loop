package previewapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
)

func (handler *Service) Claim(
	ctx context.Context,
	relayID string,
	request trafficcontrol.ClaimRequest,
) (trafficcontrol.ClaimResponse, *controlplaneapi.Error) {
	identity, session, apiError := handler.trafficSession(ctx, request.Identity)
	if apiError != nil {
		return trafficcontrol.ClaimResponse{}, apiError
	}
	task, apiError := handler.ownedTask(ctx, identity, session, request.TaskID)
	if apiError != nil {
		return trafficcontrol.ClaimResponse{}, apiError
	}
	if task.State != remotetask.Pending {
		return trafficcontrol.ClaimResponse{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: "Preview Task was already claimed",
		}
	}
	var spec storedSpec
	if json.Unmarshal(task.Spec, &spec) != nil || spec.Name == "" ||
		len(spec.Ports) == 0 {
		return trafficcontrol.ClaimResponse{}, internalError(
			errors.New("stored Preview Task is invalid"),
		)
	}
	result, _ := json.Marshal(ownerResult{OwnerID: relayID})
	if err := handler.updateTrafficTask(
		ctx, task.ID, remotetask.Pending, remotetask.Starting, result,
	); err != nil {
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
	identity, session, apiError := handler.trafficSession(ctx, request.Identity)
	if apiError != nil {
		return trafficcontrol.PrepareResponse{}, apiError
	}
	task, apiError := handler.ownedTask(ctx, identity, session, request.TaskID)
	if apiError != nil {
		return trafficcontrol.PrepareResponse{}, apiError
	}
	if task.State != remotetask.Starting || !trafficOwnedBy(task, relayID) {
		return trafficcontrol.PrepareResponse{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: previewTaskOwnershipMessage,
		}
	}
	var spec storedSpec
	if json.Unmarshal(task.Spec, &spec) != nil {
		return trafficcontrol.PrepareResponse{}, internalError(
			errors.New("stored Preview Task is invalid"),
		)
	}
	ports, err := interceptPorts(spec.Ports, request.Ports)
	if err != nil {
		return trafficcontrol.PrepareResponse{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Message: err.Error(),
			Cause:   err,
		}
	}
	claim := ownerResult{OwnerID: relayID, GatewayIP: request.GatewayIP}
	claimJSON, _ := json.Marshal(claim)
	if err := handler.updateTrafficTask(
		ctx, task.ID, remotetask.Starting, remotetask.Starting, claimJSON,
	); err != nil {
		return trafficcontrol.PrepareResponse{}, storageError(err)
	}
	snapshot := servicebinding.PreviewServiceSnapshot{
		Namespace: session.Namespace, Service: spec.Name, GatewayIP: request.GatewayIP, Ports: ports,
	}
	service, err := handler.resources.Create(ctx, identity, snapshot, task.ID)
	if err != nil {
		return trafficcontrol.PrepareResponse{}, internalError(
			fmt.Errorf("create Preview binding: %w", err),
		)
	}
	if service == nil || service.Spec.ClusterIP == "" {
		return trafficcontrol.PrepareResponse{}, internalError(
			errors.New("created Preview Service has no ClusterIP"),
		)
	}
	claim.ClusterIP = service.Spec.ClusterIP
	claimJSON, _ = json.Marshal(claim)
	if err := handler.updateTrafficTask(
		ctx, task.ID, remotetask.Starting, remotetask.Running, claimJSON,
	); err != nil {
		return trafficcontrol.PrepareResponse{}, storageError(err)
	}
	return trafficcontrol.PrepareResponse{
		ClusterIP: service.Spec.ClusterIP,
	}, nil
}
