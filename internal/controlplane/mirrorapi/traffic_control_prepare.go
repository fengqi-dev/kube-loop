package mirrorapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficsession"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

// Prepare captures the intercepted Service and, unlike Exchange, hands the
// Gateway the original backends: the shadow workload only sees a copy of the
// traffic, so the primary path has to keep reaching the real Pods (ADR 0012).
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
			internalError(fmt.Errorf("capture Mirror Service: %w", err))
	}
	backendSets, err := servicebinding.ResolveSnapshotBackends(snapshot)
	if err != nil {
		return trafficcontrol.PrepareResponse{},
			internalError(fmt.Errorf("resolve Mirror backends: %w", err))
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
			internalError(fmt.Errorf("apply Mirror binding: %w", err))
	}
	return trafficcontrol.PrepareResponse{Backends: originalBackends(backendSets)}, nil
}

func originalBackends(sets []servicebinding.BackendSet) []trafficcontrol.BackendSet {
	converted := make([]trafficcontrol.BackendSet, 0, len(sets))
	for _, set := range sets {
		backends := trafficcontrol.BackendSet{
			Name: set.Name, ServicePort: set.ServicePort,
			Protocol: strings.ToLower(string(set.Protocol)),
			Targets:  make([]trafficcontrol.BackendTarget, 0, len(set.Targets)),
		}
		for _, target := range set.Targets {
			backends.Targets = append(backends.Targets, trafficcontrol.BackendTarget{
				Address: target.Address, Port: target.Port,
			})
		}
		converted = append(converted, backends)
	}
	return converted
}
