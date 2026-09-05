package previewapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficsession"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

// Prepare creates the Preview Service rather than capturing an existing one,
// so it attaches the relay first and answers with the new ClusterIP.
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
	snapshot := servicebinding.PreviewServiceSnapshot{
		Namespace: session.Namespace, Service: trafficapi.ServiceNameFromPreview(binding),
		GatewayIP: request.GatewayIP, Ports: ports,
	}
	sessions, err := handler.bindingSessions()
	if err != nil {
		return trafficcontrol.PrepareResponse{}, apiErrors.Internal(err)
	}
	if err := sessions.AttachRelay(
		ctx, binding, relayID, request.GatewayIP, trafficapi.ListenerPorts(request.Ports),
	); err != nil {
		return trafficcontrol.PrepareResponse{}, apiErrors.Internal(err)
	}
	service, err := handler.resources.Create(ctx, identity, snapshot, binding.Spec.TaskID)
	if err != nil {
		return trafficcontrol.PrepareResponse{},
			apiErrors.Internal(fmt.Errorf("create Preview binding: %w", err))
	}
	if service == nil || service.Spec.ClusterIP == "" {
		return trafficcontrol.PrepareResponse{},
			apiErrors.Internal(errors.New("created Preview Service has no ClusterIP"))
	}
	return trafficcontrol.PrepareResponse{ClusterIP: service.Spec.ClusterIP}, nil
}
