package previewapi

import (
	"context"
	"errors"
	"fmt"
	"strings"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficsession"
	"github.com/fengqi-dev/kube-loop/internal/protocol/servicemodel"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

func (handler *Service) Claim(ctx context.Context, relayID string,
	request trafficcontrol.ClaimRequest) (trafficcontrol.ClaimResponse, *controlplaneapi.Error) {
	identity, session, apiError := handler.trafficSession(ctx, request.Identity)
	if apiError != nil {
		return trafficcontrol.ClaimResponse{}, apiError
	}
	binding, apiError := handler.ownedBinding(ctx, identity, session, request.TaskID)
	if apiError != nil {
		return trafficcontrol.ClaimResponse{}, apiError
	}
	if binding.Spec.DesiredState != trafficv1alpha1.TrafficBindingDesiredStateActive ||
		binding.Spec.Relay != nil || binding.Status.RelayOwnerID != "" {
		return trafficcontrol.ClaimResponse{}, &controlplaneapi.Error{
			Code: controlplaneapi.CodeConflict, Message: "Preview Session was already claimed",
		}
	}
	sessions, _ := handler.bindingSessions()
	if _, err := sessions.ClaimRelay(ctx, binding, relayID); err != nil {
		return trafficcontrol.ClaimResponse{}, storageError(err)
	}
	name := ""
	if binding.Spec.Preview != nil {
		name = binding.Spec.Preview.ServiceName
	}
	return trafficcontrol.ClaimResponse{
		Mode: trafficcontrol.ModePreview, TaskID: binding.Spec.TaskID,
		Service: name, Ports: append([]servicemodel.Port(nil), trafficsession.Ports(binding)...),
	}, nil
}

func (handler *Service) Prepare(ctx context.Context, relayID string,
	request trafficcontrol.PrepareRequest) (trafficcontrol.PrepareResponse, *controlplaneapi.Error) {
	identity, session, apiError := handler.trafficSession(ctx, request.Identity)
	if apiError != nil {
		return trafficcontrol.PrepareResponse{}, apiError
	}
	binding, apiError := handler.ownedBinding(ctx, identity, session, request.TaskID)
	if apiError != nil {
		return trafficcontrol.PrepareResponse{}, apiError
	}
	if binding.Status.RelayOwnerID != relayID || binding.Spec.Relay != nil {
		return trafficcontrol.PrepareResponse{}, &controlplaneapi.Error{
			Code: controlplaneapi.CodeConflict, Message: previewTaskOwnershipMessage,
		}
	}
	ports, err := interceptPorts(trafficsession.Ports(binding), request.Ports)
	if err != nil {
		return trafficcontrol.PrepareResponse{}, &controlplaneapi.Error{
			Code: controlplaneapi.CodeInvalidArgument, Message: err.Error(), Cause: err,
		}
	}
	name := ""
	if binding.Spec.Preview != nil {
		name = binding.Spec.Preview.ServiceName
	}
	snapshot := servicebinding.PreviewServiceSnapshot{
		Namespace: session.Namespace, Service: name,
		GatewayIP: request.GatewayIP, Ports: ports,
	}
	listenerPorts := make(map[string]int32, len(request.Ports))
	for _, port := range request.Ports {
		listenerPorts[strings.ToUpper(port.Protocol)+fmt.Sprintf("/%d", port.ServicePort)] = port.ListenPort
	}
	sessions, _ := handler.bindingSessions()
	if err := sessions.AttachRelay(ctx, binding, relayID, request.GatewayIP, listenerPorts); err != nil {
		return trafficcontrol.PrepareResponse{}, internalError(err)
	}
	service, err := handler.resources.Create(ctx, identity, snapshot, binding.Spec.TaskID)
	if err != nil {
		return trafficcontrol.PrepareResponse{}, internalError(fmt.Errorf("create Preview binding: %w", err))
	}
	if service == nil || service.Spec.ClusterIP == "" {
		return trafficcontrol.PrepareResponse{}, internalError(errors.New("created Preview Service has no ClusterIP"))
	}
	return trafficcontrol.PrepareResponse{ClusterIP: service.Spec.ClusterIP}, nil
}
