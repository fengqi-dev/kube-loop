package mirrorapi

import (
	"context"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/entity"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

func (handler *Service) trafficSession(
	ctx context.Context,
	ticketIdentity trafficcontrol.Identity,
) (controlplaneapi.Identity, sessionapi.ActiveSession, *controlplaneapi.Error) {
	return trafficapi.TrafficSession(ctx, handler.sessions, ticketIdentity)
}

func interceptPorts(
	expected []entity.Port,
	listeners []trafficcontrol.ListenerPort,
) ([]servicebinding.InterceptPort, error) {
	return trafficapi.InterceptPorts("Mirror", expected, listeners)
}
