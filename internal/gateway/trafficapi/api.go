package trafficapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
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
	api.serveClaimedTraffic(ctx, connection, identity, mode, claim)
}

func (api *API) reject(connection net.Conn, err error) {
	_ = tunnel.WriteStatus(connection, err)
}

func cloneIdentity(identity trafficcontrol.Identity) trafficcontrol.Identity {
	identity.Groups = append([]string(nil), identity.Groups...)
	return identity
}
