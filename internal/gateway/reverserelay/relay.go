package reverserelay

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/gateway/trafficlistener"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/transport/trafficstream"
)

var errClientStopped = errors.New("exchange stopped by client")

type relaySession struct {
	connection *trafficstream.FrameConn
	listeners  *trafficlistener.Listeners
	idle       time.Duration
	now        func() time.Time

	nextID  atomic.Uint64
	mu      sync.Mutex
	tcp     map[uint64]*tcpRelayStream
	udp     map[uint64]*udpRelayAssociation
	udpKeys map[string]uint64
}

func newRelaySession(
	connection *trafficstream.FrameConn,
	listeners *trafficlistener.Listeners,
	idle time.Duration,
	now func() time.Time,
) *relaySession {
	return &relaySession{
		connection: connection, listeners: listeners, idle: idle, now: now,
		tcp: make(map[uint64]*tcpRelayStream), udp: make(map[uint64]*udpRelayAssociation),
		udpKeys: make(map[string]uint64),
	}
}

func (relay *relaySession) run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCount := 2 + len(relay.listeners.TCP) + len(relay.listeners.UDP)
	errCh := make(chan error, errCount)
	var loopWG sync.WaitGroup
	var streamWG sync.WaitGroup
	start := func(function func() error) {
		loopWG.Go(func() {
			errCh <- function()
		})
	}
	start(func() error { return relay.readClient(ctx) })
	for _, binding := range relay.listeners.TCP {
		start(func() error { return relay.acceptTCP(ctx, binding, &streamWG) })
	}
	for index, binding := range relay.listeners.UDP {
		start(func() error { return relay.readUDP(ctx, index, binding) })
	}
	start(func() error { return relay.reapUDP(ctx) })

	var result error
	select {
	case <-ctx.Done():
		result = context.Cause(ctx)
	case result = <-errCh:
	}
	cancel()
	_ = relay.listeners.Close()
	loopWG.Wait()
	relay.closeStreams()
	streamWG.Wait()
	if result == nil && ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return result
}

func (relay *relaySession) readClient(ctx context.Context) error {
	for {
		encoded, err := relay.connection.ReadFrame(ctx)
		if err != nil {
			return err
		}
		frame, err := exchangestream.Decode(encoded)
		if err != nil {
			return err
		}
		switch frame.Type {
		case exchangestream.Data:
			stream := relay.tcpStream(frame.StreamID)
			if stream == nil {
				return errors.New("exchange data references an unknown TCP stream")
			}
			if err := writeAll(stream.connection, frame.Payload); err != nil {
				relay.removeTCP(frame.StreamID)
				_ = relay.write(ctx, exchangestream.Frame{Type: exchangestream.Close, StreamID: frame.StreamID})
			}
		case exchangestream.CloseWrite:
			stream := relay.tcpStream(frame.StreamID)
			if stream == nil {
				return errors.New("exchange half-close references an unknown TCP stream")
			}
			if connection, ok := stream.connection.(interface{ CloseWrite() error }); ok {
				_ = connection.CloseWrite()
			} else {
				_ = stream.connection.Close()
			}
		case exchangestream.Close:
			if !relay.removeStream(frame.StreamID) {
				return errors.New("exchange close references an unknown stream")
			}
		case exchangestream.Datagram:
			association := relay.udpAssociationByID(frame.StreamID)
			if association == nil {
				return errors.New("exchange datagram references an unknown UDP association")
			}
			// BindListeners validates ServicePort as a positive 16-bit port.
			servicePort := uint32(association.port.ServicePort) //nolint:gosec // Validated as a 16-bit port.
			if frame.ServicePort != servicePort {
				return errors.New("exchange datagram references an unknown UDP association")
			}
			if _, err := association.connection.WriteToUDP(frame.Payload, association.remote); err != nil {
				return err
			}
		case exchangestream.Stop:
			return errClientStopped
		case exchangestream.Ready, exchangestream.Open:
			return errors.New("client sent a server-only Exchange frame")
		default:
			return errors.New("client sent an unsupported Exchange frame")
		}
	}
}

func (relay *relaySession) write(ctx context.Context, frame exchangestream.Frame) error {
	encoded, err := exchangestream.Encode(frame)
	if err != nil {
		return err
	}
	return relay.connection.WriteFrame(ctx, encoded)
}
