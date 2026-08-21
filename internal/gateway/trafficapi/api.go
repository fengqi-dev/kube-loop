package trafficapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/gateway/mirrorrelay"
	"github.com/fengqi-dev/kube-loop/internal/gateway/reverserelay"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/mirrorstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

type ControlPlaneClient interface {
	RelayID() string
	DoJSON(context.Context, string, string, any, any) error
}

type Config struct {
	GatewayIP                string
	ControlPlane             ControlPlaneClient
	MirrorPrimaryDialContext func(context.Context, string, string) (net.Conn, error)
	HeartbeatEvery           time.Duration
	UDPIdleTimeout           time.Duration
	ShutdownTimeout          time.Duration
}

// API serves reverse traffic Tasks on authenticated logical tunnel streams.
// Task lifecycle coordination remains on the Control Plane HTTP API; only the
// Task data frames are carried by the supplied connection.
type API struct {
	config Config
}

func New(config Config) (*API, error) {
	config.GatewayIP = strings.TrimSpace(config.GatewayIP)
	if config.GatewayIP == "" || config.ControlPlane == nil || config.ControlPlane.RelayID() == "" {
		return nil, errors.New("gateway traffic API configuration is invalid")
	}
	if config.HeartbeatEvery == 0 {
		config.HeartbeatEvery = 5 * time.Second
	}
	if config.UDPIdleTimeout == 0 {
		config.UDPIdleTimeout = 30 * time.Second
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = 5 * time.Second
	}
	return &API{config: config}, nil
}

func (api *API) ServeTraffic(
	ctx context.Context,
	connection net.Conn,
	identity trafficcontrol.Identity,
	request tunnel.TrafficOpenRequest,
) {
	if connection == nil {
		return
	}
	defer func() { _ = connection.Close() }()
	if ctx == nil {
		ctx = context.Background()
	}
	mode := trafficcontrol.Mode(request.Mode)
	claimRequest := trafficcontrol.ClaimRequest{Mode: mode, TaskID: request.TaskID, Identity: cloneIdentity(identity)}
	if err := claimRequest.Validate(); err != nil {
		api.reject(connection, errors.New("traffic request is invalid"))
		return
	}

	var claim trafficcontrol.ClaimResponse
	if err := api.config.ControlPlane.DoJSON(
		ctx, http.MethodPost, trafficcontrol.InternalPathPrefix+"/claim", claimRequest, &claim,
	); err != nil {
		api.reject(connection, errors.New("ControlPlane traffic claim failed"))
		return
	}
	if claim.Mode != mode || claim.TaskID != claimRequest.TaskID || len(claim.Ports) == 0 {
		startupErr := errors.New("ControlPlane returned an invalid traffic claim")
		api.finish(context.WithoutCancel(ctx), mode, request.TaskID, true, startupErr)
		api.reject(connection, startupErr)
		return
	}

	listeners, err := reverserelay.BindListeners(api.config.GatewayIP, claim.Ports)
	if err != nil {
		startupErr := fmt.Errorf("gateway listener allocation failed: %w", err)
		api.finish(context.WithoutCancel(ctx), mode, claim.TaskID, true, startupErr)
		api.reject(connection, errors.New("gateway listener allocation failed"))
		return
	}
	defer func() { _ = listeners.Close() }()

	prepareRequest := trafficcontrol.PrepareRequest{
		Mode: mode, TaskID: claim.TaskID, Identity: cloneIdentity(identity), RelayID: api.config.ControlPlane.RelayID(),
		GatewayIP: api.config.GatewayIP, Ports: listeners.Mappings(),
	}
	var prepared trafficcontrol.PrepareResponse
	if err := api.config.ControlPlane.DoJSON(
		ctx, http.MethodPost, trafficcontrol.InternalPathPrefix+"/prepare", prepareRequest, &prepared,
	); err != nil {
		startupErr := fmt.Errorf("ControlPlane traffic preparation failed: %w", err)
		api.finish(context.WithoutCancel(ctx), mode, claim.TaskID, true, startupErr)
		api.reject(connection, errors.New("ControlPlane traffic preparation failed"))
		return
	}

	if err := tunnel.WriteStatus(connection, nil); err != nil {
		api.finish(context.WithoutCancel(ctx), mode, claim.TaskID, true, err)
		return
	}
	frameConnection, err := trafficstream.Accept(ctx, connection)
	if err != nil {
		api.finish(context.WithoutCancel(ctx), mode, claim.TaskID, true, err)
		return
	}
	defer func() { _ = frameConnection.Close() }()
	var mirror *mirrorrelay.Relay
	if mode == trafficcontrol.ModeMirror {
		mirror, err = mirrorrelay.New(frameConnection, listeners, prepared.Backends, mirrorrelay.Config{
			UDPIdleTimeout: api.config.UDPIdleTimeout, PrimaryDialContext: api.config.MirrorPrimaryDialContext,
		})
		if err != nil {
			api.finish(context.WithoutCancel(ctx), mode, claim.TaskID, true, err)
			return
		}
	}

	runContext, cancel := context.WithCancelCause(ctx)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		api.heartbeat(runContext, cancel, mode, claim.TaskID)
	}()
	if mirror != nil {
		err = mirror.Ready(runContext)
		if err == nil {
			err = mirror.Run(runContext)
		}
	} else {
		err = reverserelay.WriteFrame(runContext, frameConnection, exchangestream.Frame{Type: exchangestream.Ready})
		if err == nil {
			err = reverserelay.Run(runContext, frameConnection, listeners, api.config.UDPIdleTimeout, time.Now)
		}
	}
	failed := err != nil && !errors.Is(err, reverserelay.ErrClientStopped) &&
		!errors.Is(err, mirrorrelay.ErrClientStopped) && runContext.Err() == nil
	cancel(err)
	_ = listeners.Close()
	<-heartbeatDone
	api.finish(context.WithoutCancel(ctx), mode, claim.TaskID, failed, err)
	api.writeStop(frameConnection, mode)
}

func (api *API) heartbeat(
	ctx context.Context,
	cancel context.CancelCauseFunc,
	mode trafficcontrol.Mode,
	taskID string,
) {
	ticker := time.NewTicker(api.config.HeartbeatEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		requestContext, requestCancel := context.WithTimeout(ctx, api.config.HeartbeatEvery)
		var response trafficcontrol.HeartbeatResponse
		err := api.config.ControlPlane.DoJSON(
			requestContext,
			http.MethodPut,
			trafficcontrol.InternalPathPrefix+"/heartbeat",
			trafficcontrol.HeartbeatRequest{Mode: mode, TaskID: taskID, RelayID: api.config.ControlPlane.RelayID()},
			&response,
		)
		requestCancel()
		if err != nil {
			cancel(fmt.Errorf("traffic heartbeat: %w", err))
			return
		}
		if response.Stop {
			cancel(errors.New("traffic Task stop requested"))
			return
		}
	}
}

func (api *API) finish(ctx context.Context, mode trafficcontrol.Mode, taskID string, failed bool, cause error) {
	finishContext, cancel := context.WithTimeout(ctx, api.config.ShutdownTimeout)
	defer cancel()
	reason := ""
	if cause != nil {
		reason = strings.TrimSpace(cause.Error())
		if len(reason) > 512 {
			reason = reason[:512]
		}
	}
	var response trafficcontrol.FinishResponse
	_ = api.config.ControlPlane.DoJSON(
		finishContext,
		http.MethodPost,
		trafficcontrol.InternalPathPrefix+"/finish",
		trafficcontrol.FinishRequest{
			Mode: mode, TaskID: taskID, RelayID: api.config.ControlPlane.RelayID(), Failed: failed, Reason: reason,
		},
		&response,
	)
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

func (api *API) reject(connection net.Conn, err error) {
	_ = tunnel.WriteStatus(connection, err)
}

func cloneIdentity(identity trafficcontrol.Identity) trafficcontrol.Identity {
	identity.Groups = append([]string(nil), identity.Groups...)
	return identity
}
