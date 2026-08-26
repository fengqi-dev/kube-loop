package reverserelay

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/entity"
	"github.com/fengqi-dev/kube-loop/internal/gateway/trafficlistener"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
)

type tcpRelayStream struct {
	connection net.Conn
	port       entity.Port
}

func (relay *relaySession) acceptTCP(
	ctx context.Context,
	binding trafficlistener.TCPBinding,
	streamWG *sync.WaitGroup,
) error {
	for {
		connection, err := binding.Listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil //nolint:nilerr // A closed listener is the normal relay shutdown signal.
			}
			return err
		}
		id := relay.nextStreamID()
		relay.mu.Lock()
		relay.tcp[id] = &tcpRelayStream{connection: connection, port: binding.Port}
		relay.mu.Unlock()
		if err := relay.write(ctx, exchangestream.Frame{
			Type: exchangestream.Open, StreamID: id,
			// BindListeners validates ServicePort as a positive 16-bit port.
			ServicePort: uint32(binding.Port.ServicePort), //nolint:gosec // Validated as a positive 16-bit port.
			Protocol:    exchangestream.ProtocolTCP,
		}); err != nil {
			relay.removeTCP(id)
			return err
		}
		streamWG.Go(func() {
			relay.readTCP(ctx, id, connection)
		})
	}
}

func (relay *relaySession) readTCP(ctx context.Context, id uint64, connection net.Conn) {
	buffer := make([]byte, 32<<10)
	for {
		count, err := connection.Read(buffer)
		if count > 0 {
			payload := append([]byte(nil), buffer[:count]...)
			frame := exchangestream.Frame{Type: exchangestream.Data, StreamID: id, Payload: payload}
			if writeErr := relay.write(ctx, frame); writeErr != nil {
				relay.removeTCP(id)
				return
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			_ = relay.write(ctx, exchangestream.Frame{Type: exchangestream.CloseWrite, StreamID: id})
			return
		}
		_ = relay.write(ctx, exchangestream.Frame{Type: exchangestream.Close, StreamID: id})
		relay.removeTCP(id)
		return
	}
}
