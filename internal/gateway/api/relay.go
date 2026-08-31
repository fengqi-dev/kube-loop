package api

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/gateway/relay/listener"
	"github.com/fengqi-dev/kube-loop/internal/gateway/relay/mirror"
	"github.com/fengqi-dev/kube-loop/internal/gateway/relay/reverse"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/mirrorstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/transport/trafficstream"
)

type trafficRelay interface {
	Ready(context.Context) error
	Run(context.Context) error
}

type exchangeRelay struct {
	connection     *trafficstream.FrameConn
	listeners      *listener.Listeners
	udpIdleTimeout time.Duration
}

func (relay *exchangeRelay) Ready(ctx context.Context) error {
	return reverse.WriteFrame(ctx, relay.connection, exchangestream.Frame{Type: exchangestream.Ready})
}

func (relay *exchangeRelay) Run(ctx context.Context) error {
	return reverse.Run(ctx, relay.connection, relay.listeners, relay.udpIdleTimeout, time.Now)
}

func (api *API) serveRelay(
	ctx context.Context,
	connection net.Conn,
	mode trafficcontrol.Mode,
	taskID string,
	listeners *listener.Listeners,
	prepared trafficcontrol.PrepareResponse,
) {
	if err := tunnel.WriteStatus(connection, nil); err != nil {
		api.finish(context.WithoutCancel(ctx), mode, taskID, true, err)
		return
	}
	encryptionEnabled := true
	if api.config.TrafficEncryption != nil {
		encryptionEnabled = *api.config.TrafficEncryption
	}
	frameConnection, err := trafficstream.AcceptWithEncryptionStatic(
		ctx, connection, encryptionEnabled, noiseStaticKey(api.config.NoiseStaticKey),
	)
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

func noiseStaticKey(key *trafficstream.NoiseStaticKeypair) trafficstream.NoiseStaticKeypair {
	if key == nil {
		return trafficstream.NoiseStaticKeypair{}
	}
	return *key
}

func (api *API) newRelay(
	mode trafficcontrol.Mode,
	connection *trafficstream.FrameConn,
	listeners *listener.Listeners,
	prepared trafficcontrol.PrepareResponse,
) (trafficRelay, error) {
	if mode == trafficcontrol.ModeMirror {
		return mirror.New(connection, listeners, prepared.Backends, mirror.Config{
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
	listeners *listener.Listeners,
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
	relayFailed := err != nil && !errors.Is(err, reverse.ErrClientStopped) &&
		!errors.Is(err, mirror.ErrClientStopped) && runContext.Err() == nil
	cancel(err)
	cause := context.Cause(runContext)
	failed := relayFailed || errors.Is(cause, errHeartbeatFailed)
	_ = listeners.Close()
	<-heartbeatDone
	api.finish(context.WithoutCancel(ctx), mode, taskID, failed, cause)
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
