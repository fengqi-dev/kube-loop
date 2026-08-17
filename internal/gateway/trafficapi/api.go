package trafficapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/gateway/mirrorrelay"
	"github.com/fengqi-dev/kube-loop/internal/gateway/reverserelay"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/mirrorstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
	"github.com/labstack/echo/v5"
)

type ControlPlaneClient interface {
	RelayID() string
	DoJSON(context.Context, string, string, any, any) error
}

type Config struct {
	GatewayIP                string
	VerifyRequest            func(*http.Request) (relayticket.Claims, error)
	ControlPlane             ControlPlaneClient
	MirrorPrimaryDialContext func(context.Context, string, string) (net.Conn, error)
	HeartbeatEvery           time.Duration
	UDPIdleTimeout           time.Duration
	ShutdownTimeout          time.Duration
	MaximumSessions          int
	OtherSessions            func() int
}

type API struct {
	config   Config
	active   atomic.Int64
	draining atomic.Bool
}

func New(config Config) (*API, error) {
	config.GatewayIP = strings.TrimSpace(config.GatewayIP)
	if config.GatewayIP == "" || config.VerifyRequest == nil || config.ControlPlane == nil || config.ControlPlane.RelayID() == "" {
		return nil, errors.New("Gateway traffic API configuration is invalid")
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
	if config.MaximumSessions == 0 {
		config.MaximumSessions = 256
	}
	if config.MaximumSessions < 1 {
		return nil, errors.New("Gateway traffic session limit is invalid")
	}
	return &API{config: config}, nil
}

func (api *API) RegisterRoutes(router *echo.Echo) {
	router.GET(trafficcontrol.PublicPathPrefix+"/:mode/:taskID", api.stream)
}

func (api *API) stream(ctx *echo.Context) error {
	if api.draining.Load() {
		return ctx.JSON(http.StatusServiceUnavailable, errorDocument("unavailable", "Gateway is draining"))
	}
	active := api.active.Add(1)
	other := 0
	if api.config.OtherSessions != nil {
		other = api.config.OtherSessions()
	}
	if active+int64(other) > int64(api.config.MaximumSessions) {
		api.active.Add(-1)
		return ctx.JSON(http.StatusServiceUnavailable, errorDocument("unavailable", "Gateway traffic capacity is exhausted"))
	}
	defer api.active.Add(-1)
	request := ctx.Request()
	claims, err := api.config.VerifyRequest(request)
	if err != nil {
		return ctx.JSON(http.StatusUnauthorized, errorDocument("unauthenticated", "RelayTicket is invalid"))
	}
	mode := trafficcontrol.Mode(ctx.Param("mode"))
	identity := trafficcontrol.Identity{
		IdentityID: claims.IdentityID, Groups: append([]string(nil), claims.Groups...), DeviceID: claims.DeviceID,
		SessionID: claims.SessionID, SessionGeneration: claims.SessionGeneration, Namespace: claims.Namespace,
	}
	claimRequest := trafficcontrol.ClaimRequest{Mode: mode, TaskID: ctx.Param("taskID"), Identity: identity}
	if err := claimRequest.Validate(); err != nil {
		return ctx.JSON(http.StatusBadRequest, errorDocument("invalid_argument", "traffic request is invalid"))
	}
	var claim trafficcontrol.ClaimResponse
	if err := api.config.ControlPlane.DoJSON(request.Context(), http.MethodPost, trafficcontrol.InternalPathPrefix+"/claim", claimRequest, &claim); err != nil {
		return api.controlError(ctx, err)
	}
	if claim.Mode != mode || claim.TaskID != claimRequest.TaskID || len(claim.Ports) == 0 {
		return ctx.JSON(http.StatusBadGateway, errorDocument("unavailable", "ControlPlane returned an invalid traffic claim"))
	}
	listeners, err := reverserelay.BindListeners(api.config.GatewayIP, claim.Ports)
	if err != nil {
		api.finish(context.Background(), mode, claim.TaskID, true, err)
		return ctx.JSON(http.StatusServiceUnavailable, errorDocument("unavailable", "Gateway listener allocation failed"))
	}
	defer listeners.Close()
	connection, err := websocket.Accept(ctx.Response(), request, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		api.finish(context.Background(), mode, claim.TaskID, true, errors.New("WebSocket upgrade failed"))
		return nil
	}
	defer connection.CloseNow()
	if mode == trafficcontrol.ModeMirror {
		connection.SetReadLimit(mirrorstream.MaximumData + mirrorstream.HeaderSize)
	} else {
		connection.SetReadLimit(exchangestream.MaximumData + 14)
	}

	prepareRequest := trafficcontrol.PrepareRequest{
		Mode: mode, TaskID: claim.TaskID, Identity: identity, RelayID: api.config.ControlPlane.RelayID(),
		GatewayIP: api.config.GatewayIP, Ports: listeners.Mappings(),
	}
	var prepared trafficcontrol.PrepareResponse
	if err := api.config.ControlPlane.DoJSON(request.Context(), http.MethodPost, trafficcontrol.InternalPathPrefix+"/prepare", prepareRequest, &prepared); err != nil {
		api.finish(context.Background(), mode, claim.TaskID, true, err)
		_ = connection.Close(websocket.StatusInternalError, "traffic preparation failed")
		return nil
	}
	runContext, cancel := context.WithCancelCause(request.Context())
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		api.heartbeat(runContext, cancel, connection, mode, claim.TaskID)
	}()
	if mode == trafficcontrol.ModeMirror {
		var relay *mirrorrelay.Relay
		relay, err = mirrorrelay.New(connection, listeners, prepared.Backends, mirrorrelay.Config{
			UDPIdleTimeout: api.config.UDPIdleTimeout, PrimaryDialContext: api.config.MirrorPrimaryDialContext,
		})
		if err == nil {
			err = relay.Ready(runContext)
		}
		if err == nil {
			err = relay.Run(runContext)
		}
	} else {
		err = reverserelay.WriteFrame(runContext, connection, exchangestream.Frame{Type: exchangestream.Ready})
		if err == nil {
			err = reverserelay.Run(runContext, connection, listeners, api.config.UDPIdleTimeout, time.Now)
		}
	}
	failed := err != nil && !errors.Is(err, reverserelay.ErrClientStopped) &&
		!errors.Is(err, mirrorrelay.ErrClientStopped) && runContext.Err() == nil
	cancel(err)
	_ = listeners.Close()
	<-heartbeatDone
	api.finish(context.Background(), mode, claim.TaskID, failed, err)
	closeContext, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	stop, _ := exchangestream.Encode(exchangestream.Frame{Type: exchangestream.Stop})
	if mode == trafficcontrol.ModeMirror {
		stop, _ = mirrorstream.Encode(mirrorstream.Frame{Type: mirrorstream.Stop})
	}
	_ = connection.Write(closeContext, websocket.MessageBinary, stop)
	closeCancel()
	if failed {
		_ = connection.Close(websocket.StatusInternalError, "traffic stream failed")
	} else {
		_ = connection.Close(websocket.StatusNormalClosure, "traffic stopped")
	}
	return nil
}

func (api *API) ActiveSessions() int { return int(api.active.Load()) }

func (api *API) Draining() bool { return api.draining.Load() }

func (api *API) BeginDrain() { api.draining.Store(true) }

func (api *API) heartbeat(
	ctx context.Context,
	cancel context.CancelCauseFunc,
	connection *websocket.Conn,
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
		if err := connection.Ping(requestContext); err != nil {
			requestCancel()
			cancel(fmt.Errorf("traffic WebSocket keepalive: %w", err))
			return
		}
		var response trafficcontrol.HeartbeatResponse
		err := api.config.ControlPlane.DoJSON(requestContext, http.MethodPut, trafficcontrol.InternalPathPrefix+"/heartbeat", trafficcontrol.HeartbeatRequest{
			Mode: mode, TaskID: taskID, RelayID: api.config.ControlPlane.RelayID(),
		}, &response)
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
	_ = api.config.ControlPlane.DoJSON(finishContext, http.MethodPost, trafficcontrol.InternalPathPrefix+"/finish", trafficcontrol.FinishRequest{
		Mode: mode, TaskID: taskID, RelayID: api.config.ControlPlane.RelayID(), Failed: failed, Reason: reason,
	}, &response)
}

func (api *API) controlError(ctx *echo.Context, err error) error {
	status := http.StatusBadGateway
	var statusError interface{ HTTPStatus() int }
	if errors.As(err, &statusError) {
		status = statusError.HTTPStatus()
	}
	return ctx.JSON(status, errorDocument("unavailable", "ControlPlane traffic control request failed"))
}

func errorDocument(code, message string) map[string]any {
	return map[string]any{"error": map[string]string{"code": code, "message": message}}
}
