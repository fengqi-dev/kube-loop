package exchangeapi

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
			Message: "Exchange Task was already claimed",
		}
	}
	var spec storedSpec
	if json.Unmarshal(task.Spec, &spec) != nil || spec.Service == "" ||
		len(spec.Ports) == 0 {
		return trafficcontrol.ClaimResponse{}, internalError(
			errors.New("stored Exchange Task is invalid"),
		)
	}
	result, _ := json.Marshal(ownerResult{OwnerID: relayID})
	if err := handler.updateTrafficTask(
		ctx, task.ID, remotetask.Pending, remotetask.Starting, result,
	); err != nil {
		return trafficcontrol.ClaimResponse{}, storageError(err)
	}
	return trafficcontrol.ClaimResponse{
		Mode: trafficcontrol.ModeExchange, TaskID: task.ID, Service: spec.Service,
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
			Message: exchangeTaskOwnershipMessage,
		}
	}
	var spec storedSpec
	if json.Unmarshal(task.Spec, &spec) != nil {
		return trafficcontrol.PrepareResponse{}, internalError(
			errors.New("stored Exchange Task is invalid"),
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
	snapshot := servicebinding.ServiceInterceptSnapshot{
		Namespace: session.Namespace, Service: spec.Service, GatewayIP: request.GatewayIP, Ports: ports,
	}
	if err := handler.resources.Capture(ctx, identity, &snapshot); err != nil {
		return trafficcontrol.PrepareResponse{}, internalError(
			fmt.Errorf("capture Exchange Service: %w", err),
		)
	}
	if err := handler.resources.Apply(ctx, identity, snapshot, task.ID); err != nil {
		return trafficcontrol.PrepareResponse{}, internalError(
			fmt.Errorf("apply Exchange binding: %w", err),
		)
	}
	if err := handler.updateTrafficTask(
		ctx, task.ID, remotetask.Starting, remotetask.Running, claimJSON,
	); err != nil {
		return trafficcontrol.PrepareResponse{}, storageError(err)
	}
	return trafficcontrol.PrepareResponse{}, nil
}
