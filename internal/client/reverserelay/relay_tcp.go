package reverserelay

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

func (relay *Relay) openTCP(ctx context.Context, frame exchangestream.Frame) error {
	servicePort, err := reverseServicePort(frame.ServicePort)
	if err != nil {
		return err
	}
	target, exists := relay.targets[utils.TargetKey("tcp", servicePort)]
	if !exists {
		return errors.New("gateway requested an unconfigured local TCP target")
	}
	if relay.hasStream(frame.StreamID) {
		return errors.New("gateway reused an active reverse stream ID")
	}
	connection, err := relay.dial(ctx, "tcp", localAddress(target))
	if err != nil {
		return relay.write(ctx, exchangestream.Frame{Type: exchangestream.Close, StreamID: frame.StreamID})
	}
	relay.mu.Lock()
	relay.tcp[frame.StreamID] = &localConnection{connection: connection, target: target}
	relay.mu.Unlock()
	relay.wg.Go(func() {
		relay.readTCP(ctx, frame.StreamID, connection)
	})
	return nil
}

func (relay *Relay) readTCP(ctx context.Context, id uint64, connection net.Conn) {
	buffer := make([]byte, 32<<10)
	for {
		count, err := connection.Read(buffer)
		if count > 0 {
			if writeErr := relay.write(ctx, exchangestream.Frame{
				Type: exchangestream.Data, StreamID: id, Payload: append([]byte(nil), buffer[:count]...),
			}); writeErr != nil {
				relay.remove(relay.tcp, id)
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
		relay.remove(relay.tcp, id)
		return
	}
}
