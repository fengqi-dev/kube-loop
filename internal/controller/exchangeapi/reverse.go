package exchangeapi

import (
	"context"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/servicebinding"
)

// ReverseListeners is the transport-only listener set shared by Exchange and
// Preview until their Task plumbing is unified. Kubernetes ownership
// and Task state remain outside this type.
type ReverseListeners struct{ listeners *boundListeners }

// ErrReverseClientStopped identifies the authenticated client's Stop frame.
var ErrReverseClientStopped = errClientStopped

func BindReverseListeners(gatewayIP string, ports []Port) (*ReverseListeners, error) {
	listeners, err := bindExchangeListeners(gatewayIP, ports)
	if err != nil {
		return nil, err
	}
	return &ReverseListeners{listeners: listeners}, nil
}

func (listeners *ReverseListeners) Mappings() []servicebinding.InterceptPort {
	if listeners == nil || listeners.listeners == nil {
		return nil
	}
	return append([]servicebinding.InterceptPort(nil), listeners.listeners.mappings...)
}

func (listeners *ReverseListeners) Close() error {
	if listeners == nil {
		return nil
	}
	return listeners.listeners.Close()
}

func WriteReverseFrame(ctx context.Context, connection *websocket.Conn, frame exchangestream.Frame) error {
	relay := &relaySession{connection: connection}
	return relay.write(ctx, frame)
}

func RunReverseRelay(
	ctx context.Context,
	connection *websocket.Conn,
	listeners *ReverseListeners,
	udpIdleTimeout time.Duration,
	now func() time.Time,
) error {
	return newRelaySession(connection, listeners.listeners, udpIdleTimeout, now).run(ctx)
}
