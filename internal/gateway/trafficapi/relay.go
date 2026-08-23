package trafficapi

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/gateway/mirrorrelay"
	"github.com/fengqi-dev/kube-loop/internal/gateway/reverserelay"
	"github.com/fengqi-dev/kube-loop/internal/gateway/trafficlistener"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/mirrorstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

type trafficRelay interface {
	Ready(context.Context) error
	Run(context.Context) error
}

type exchangeRelay struct {
	connection     *trafficstream.FrameConn
	listeners      *trafficlistener.Listeners
	udpIdleTimeout time.Duration
}

func (relay *exchangeRelay) Ready(ctx context.Context) error {
	return reverserelay.WriteFrame(ctx, relay.connection, exchangestream.Frame{Type: exchangestream.Ready})
}

func (relay *exchangeRelay) Run(ctx context.Context) error {
	return reverserelay.Run(ctx, relay.connection, relay.listeners, relay.udpIdleTimeout, time.Now)
}

func (api *API) serveRelay(
	ctx context.Context,
	connection net.Conn,
	mode trafficcontrol.Mode,
	taskID string,
	listeners *trafficlistener.Listeners,
	prepared trafficcontrol.PrepareResponse,
) {
	if err := tunnel.WriteStatus(connection, nil); err != nil {
		api.finish(context.WithoutCancel(ctx), mode, taskID, true, err)
		return
	}
	frameConnection, err := trafficstream.Accept(ctx, connection)
	if err != nil {
		api.finish(context.WithoutCancel(ctx), mode, taskID, true, err)
		return
	}
	defer func() { _ = frameConnection.Close() }()

	relay, err := api.newRelay(mode, frameConnection, listeners, prepared)
	if err != nil {
		api.finish(context.WithoutCancel(ctx), mode, taskID, true, err)
		return
	}
	api.runRelay(ctx, frameConnection, relay, mode, taskID, listeners)
}

func (api *API) newRelay(
	mode trafficcontrol.Mode,
	connection *trafficstream.FrameConn,
	listeners *trafficlistener.Listeners,
	prepared trafficcontrol.PrepareResponse,
) (trafficRelay, error) {
	if mode == trafficcontrol.ModeMirror {
		return mirrorrelay.New(connection, listeners, prepared.Backends, mirrorrelay.Config{
			UDPIdleTimeout:     api.config.UDPIdleTimeout,
			PrimaryDialContext: api.config.MirrorPrimaryDialContext,
		})
	}
	return &exchangeRelay{
		connection: connection, listeners: listeners, udpIdleTimeout: api.config.UDPIdleTimeout,
	}, nil
}

func (api *API) runRelay(
	ctx context.Context,
	connection *trafficstream.FrameConn,
	relay trafficRelay,
	mode trafficcontrol.Mode,
	taskID string,
	listeners *trafficlistener.Listeners,
) {
	runContext, cancel := context.WithCancelCause(ctx)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		api.heartbeat(runContext, cancel, mode, taskID)
	}()

	err := relay.Ready(runContext)
	if err == nil {
		err = relay.Run(runContext)
	}
	failed := err != nil && !errors.Is(err, reverserelay.ErrClientStopped) &&
		!errors.Is(err, mirrorrelay.ErrClientStopped) && runContext.Err() == nil
	cancel(err)
	_ = listeners.Close()
	<-heartbeatDone
	api.finish(context.WithoutCancel(ctx), mode, taskID, failed, err)
	api.writeStop(connection, mode)
}

func (api *API) writeStop(connection *trafficstream.FrameConn, mode trafficcontrol.Mode) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if mode == trafficcontrol.ModeMirror {
		encoded, err := mirrorstream.Encode(mirrorstream.Frame{Type: mirrorstream.Stop})
		if err == nil {
			_ = connection.WriteFrame(ctx, encoded)
		}
		return
	}
	encoded, err := exchangestream.Encode(exchangestream.Frame{Type: exchangestream.Stop})
	if err == nil {
		_ = connection.WriteFrame(ctx, encoded)
	}
}
