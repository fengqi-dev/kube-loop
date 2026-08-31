package reverse

import (
	"context"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/entity"
	"github.com/fengqi-dev/kube-loop/internal/gateway/relay/listener"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/transport/trafficstream"
)

var ErrClientStopped = errClientStopped

func BindListeners(gatewayIP string, ports []entity.Port) (*listener.Listeners, error) {
	return listener.Bind(gatewayIP, ports)
}

func WriteFrame(ctx context.Context, connection *trafficstream.FrameConn, frame exchangestream.Frame) error {
	relay := &relaySession{connection: connection}
	return relay.write(ctx, frame)
}

func Run(
	ctx context.Context,
	connection *trafficstream.FrameConn,
	listeners *listener.Listeners,
	udpIdleTimeout time.Duration,
	now func() time.Time,
) error {
	return newRelaySession(connection, listeners, udpIdleTimeout, now).run(ctx)
}
