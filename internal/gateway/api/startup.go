package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/fengqi-dev/kube-loop/internal/gateway/relay/reverse"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

func (api *API) serveClaimedTraffic(
	ctx context.Context,
	connection net.Conn,
	identity trafficcontrol.Identity,
	mode trafficcontrol.Mode,
	claim trafficcontrol.ClaimResponse,
) {
	listeners, err := reverse.BindListeners(api.config.GatewayIP, claim.Ports)
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

	api.serveRelay(ctx, connection, mode, claim.TaskID, listeners, prepared)
}
