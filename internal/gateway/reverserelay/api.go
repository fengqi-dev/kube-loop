package reverserelay

import (
	"context"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/gateway/trafficlistener"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
)

var ErrClientStopped = errClientStopped

func BindListeners(gatewayIP string, ports []trafficmodel.Port) (*trafficlistener.Listeners, error) {
	return trafficlistener.Bind(gatewayIP, ports)
}

func WriteFrame(ctx context.Context, connection *websocket.Conn, frame exchangestream.Frame) error {
	relay := &relaySession{connection: connection}
	return relay.write(ctx, frame)
}

func Run(
	ctx context.Context,
	connection *websocket.Conn,
	listeners *trafficlistener.Listeners,
	udpIdleTimeout time.Duration,
	now func() time.Time,
) error {
	return newRelaySession(connection, listeners, udpIdleTimeout, now).run(ctx)
}
