package exchangeapi

import (
	"context"
	"fmt"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficsession"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

// Prepare captures the target Service, points it at the Gateway's listeners
// and activates the binding. Claim, Heartbeat and Finish are the same for
// every traffic task and come from the embedded trafficapi.Relay.
func (handler *Service) Prepare(
	ctx context.Context,
	relayID string,
	request trafficcontrol.PrepareRequest,
) (trafficcontrol.PrepareResponse, *controlplaneapi.Error) {
	identity, session, binding, apiError := handler.PrepareBinding(ctx, relayID, request)
	if apiError != nil {
		return trafficcontrol.PrepareResponse{}, apiError
	}
	ports, err := interceptPorts(trafficsession.Ports(binding), request.Ports)
	if err != nil {
		return trafficcontrol.PrepareResponse{}, &controlplaneapi.Error{
			Code: controlplaneapi.CodeInvalidArgument, Message: err.Error(), Cause: err,
		}
	}
	snapshot := servicebinding.ServiceInterceptSnapshot{
		Namespace: session.Namespace, Service: trafficapi.ServiceNameFromTarget(binding),
		GatewayIP: request.GatewayIP, Ports: ports,
	}
	if err := handler.resources.Capture(ctx, identity, &snapshot); err != nil {
		return trafficcontrol.PrepareResponse{},
			internalError(fmt.Errorf("capture Exchange Service: %w", err))
	}
	sessions, err := handler.bindingSessions()
	if err != nil {
		return trafficcontrol.PrepareResponse{}, internalError(err)
	}
	if err := sessions.AttachRelay(
		ctx, binding, relayID, request.GatewayIP, trafficapi.ListenerPorts(request.Ports),
	); err != nil {
		return trafficcontrol.PrepareResponse{}, internalError(err)
	}
	if err := handler.resources.Apply(ctx, identity, snapshot, binding.Spec.TaskID); err != nil {
		return trafficcontrol.PrepareResponse{},
			internalError(fmt.Errorf("apply Exchange binding: %w", err))
	}
	return trafficcontrol.PrepareResponse{}, nil
}
