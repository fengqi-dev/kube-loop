package reverserelay

import (
	"context"
	"errors"
	"io"

	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

func (relay *Relay) datagram(ctx context.Context, frame exchangestream.Frame) error {
	servicePort, err := reverseServicePort(frame.ServicePort)
	if err != nil {
		return err
	}
	target, exists := relay.targets[utils.TargetKey("udp", servicePort)]
	if !exists {
		return errors.New("gateway requested an unconfigured local UDP target")
	}
	association := relay.connection(relay.udp, frame.StreamID)
	if association == nil {
		if relay.hasStream(frame.StreamID) {
			return errors.New("gateway reused an active reverse stream ID")
		}
		connection, err := relay.dial(ctx, "udp", localAddress(target))
		if err != nil {
			return relay.write(ctx, exchangestream.Frame{Type: exchangestream.Close, StreamID: frame.StreamID})
		}
		association = &localConnection{connection: connection, target: target}
		relay.mu.Lock()
		relay.udp[frame.StreamID] = association
		relay.mu.Unlock()
		relay.wg.Go(func() {
			relay.readUDP(ctx, frame.StreamID, association)
		})
	}
	if association.target.ServicePort != servicePort {
		return errors.New("gateway changed a reverse UDP association port")
	}
	count, err := association.connection.Write(frame.Payload)
	if err != nil {
		return err
	}
	if count != len(frame.Payload) {
		return io.ErrShortWrite
	}
	return nil
}

func (relay *Relay) readUDP(ctx context.Context, id uint64, association *localConnection) {
	servicePort, err := encodedReverseServicePort(association.target.ServicePort)
	if err != nil {
		relay.remove(relay.udp, id)
		return
	}
	buffer := make([]byte, 65507)
	for {
		count, err := association.connection.Read(buffer)
		if err != nil {
			relay.remove(relay.udp, id)
			return
		}
		if count == 0 {
			continue
		}
		if err := relay.write(ctx, exchangestream.Frame{
			Type: exchangestream.Datagram, StreamID: id,
			ServicePort: servicePort, Protocol: exchangestream.ProtocolUDP,
			Payload: append([]byte(nil), buffer[:count]...),
		}); err != nil {
			relay.remove(relay.udp, id)
			return
		}
	}
}
