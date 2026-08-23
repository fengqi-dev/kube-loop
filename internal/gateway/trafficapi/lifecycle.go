package trafficapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

var (
	errHeartbeatFailed   = errors.New("traffic heartbeat")
	errTaskStopRequested = errors.New("traffic Task stop requested")
)

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
			cancel(fmt.Errorf("%w: %w", errHeartbeatFailed, err))
			return
		}
		if response.Stop {
			cancel(errTaskStopRequested)
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
