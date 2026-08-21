package trafficcontrolapi

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

type ModeCoordinator = Coordinator

type Dispatcher struct {
	modes map[trafficcontrol.Mode]ModeCoordinator
}

func NewDispatcher(
	exchange, mirror, preview ModeCoordinator,
) (*Dispatcher, error) {
	if exchange == nil || mirror == nil || preview == nil {
		return nil, errors.New(
			"exchange, Mirror and Preview traffic coordinators are required",
		)
	}
	return &Dispatcher{modes: map[trafficcontrol.Mode]ModeCoordinator{
		trafficcontrol.ModeExchange: exchange,
		trafficcontrol.ModeMirror:   mirror,
		trafficcontrol.ModePreview:  preview,
	}}, nil
}

func (dispatcher *Dispatcher) Claim(
	ctx context.Context,
	relayID string,
	request trafficcontrol.ClaimRequest,
) (trafficcontrol.ClaimResponse, *controlplaneapi.Error) {
	coordinator := dispatcher.modes[request.Mode]
	if coordinator == nil {
		return trafficcontrol.ClaimResponse{}, invalid(
			"traffic mode is unsupported",
		)
	}
	return coordinator.Claim(ctx, relayID, request)
}

func (dispatcher *Dispatcher) Prepare(
	ctx context.Context,
	relayID string,
	request trafficcontrol.PrepareRequest,
) (trafficcontrol.PrepareResponse, *controlplaneapi.Error) {
	coordinator := dispatcher.modes[request.Mode]
	if coordinator == nil {
		return trafficcontrol.PrepareResponse{}, invalid(
			"traffic mode is unsupported",
		)
	}
	return coordinator.Prepare(ctx, relayID, request)
}

func (dispatcher *Dispatcher) Heartbeat(
	ctx context.Context,
	relayID string,
	request trafficcontrol.HeartbeatRequest,
) (trafficcontrol.HeartbeatResponse, *controlplaneapi.Error) {
	coordinator := dispatcher.modes[request.Mode]
	if coordinator == nil {
		return trafficcontrol.HeartbeatResponse{}, invalid(
			"traffic mode is unsupported",
		)
	}
	return coordinator.Heartbeat(ctx, relayID, request)
}

func (dispatcher *Dispatcher) Finish(
	ctx context.Context,
	relayID string,
	request trafficcontrol.FinishRequest,
) (trafficcontrol.FinishResponse, *controlplaneapi.Error) {
	coordinator := dispatcher.modes[request.Mode]
	if coordinator == nil {
		return trafficcontrol.FinishResponse{}, invalid(
			"traffic mode is unsupported",
		)
	}
	return coordinator.Finish(ctx, relayID, request)
}
