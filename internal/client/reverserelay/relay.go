package reverserelay

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficstream"
)

// Target is a client-retained destination for one Service port. It is never
// accepted from the Gateway after the reverse stream has been established.
type Target struct {
	ServicePort int32  `json:"servicePort"`
	Protocol    string `json:"protocol"`
	LocalHost   string `json:"localHost"`
	LocalPort   uint16 `json:"localPort"`
}

type DialContextFunc func(context.Context, string, string) (net.Conn, error)

// Relay translates the authenticated Exchange wire protocol into connections
// to the immutable local targets supplied by the desktop client.
type Relay struct {
	stream  *trafficstream.FrameConn
	targets map[string]Target
	dial    DialContextFunc

	mu  sync.Mutex
	tcp map[uint64]*localConnection
	udp map[uint64]*localConnection
	wg  sync.WaitGroup
}

func New(connection *trafficstream.FrameConn, targets []Target, dial DialContextFunc) *Relay {
	targetMap := make(map[string]Target, len(targets))
	for _, target := range targets {
		targetMap[targetKey(target.Protocol, target.ServicePort)] = target
	}
	return &Relay{
		stream: connection, targets: targetMap, dial: dial,
		tcp: make(map[uint64]*localConnection), udp: make(map[uint64]*localConnection),
	}
}

func (relay *Relay) ReadReady(ctx context.Context) error {
	encoded, err := relay.stream.ReadFrame(ctx)
	if err != nil {
		return err
	}
	frame, err := exchangestream.Decode(encoded)
	if err != nil || frame.Type != exchangestream.Ready {
		return errors.New("gateway returned an invalid reverse readiness frame")
	}
	return nil
}

func (relay *Relay) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		relay.closeAll()
		relay.wg.Wait()
		_ = relay.stream.Close()
	}()
	for {
		encoded, err := relay.stream.ReadFrame(ctx)
		if err != nil {
			return err
		}
		frame, err := exchangestream.Decode(encoded)
		if err != nil {
			return err
		}
		switch frame.Type {
		case exchangestream.Open:
			if err := relay.openTCP(ctx, frame); err != nil {
				return err
			}
		case exchangestream.Data:
			stream := relay.connection(relay.tcp, frame.StreamID)
			if stream == nil {
				return errors.New("gateway referenced an unknown local TCP stream")
			}
			if err := writeLocal(stream.connection, frame.Payload); err != nil {
				relay.remove(relay.tcp, frame.StreamID)
				_ = relay.write(ctx, exchangestream.Frame{Type: exchangestream.Close, StreamID: frame.StreamID})
			}
		case exchangestream.CloseWrite:
			stream := relay.connection(relay.tcp, frame.StreamID)
			if stream == nil {
				return errors.New("gateway referenced an unknown local TCP stream")
			}
			if connection, ok := stream.connection.(interface{ CloseWrite() error }); ok {
				_ = connection.CloseWrite()
			} else {
				_ = stream.connection.Close()
			}
		case exchangestream.Close:
			if !relay.removeAny(frame.StreamID) {
				return errors.New("gateway referenced an unknown local reverse stream")
			}
		case exchangestream.Datagram:
			if err := relay.datagram(ctx, frame); err != nil {
				return err
			}
		case exchangestream.Stop:
			return nil
		case exchangestream.Ready:
			return errors.New("gateway sent duplicate reverse readiness")
		default:
			return errors.New("gateway sent a client-only reverse frame")
		}
	}
}

func (relay *Relay) Stop(ctx context.Context) error {
	return relay.write(ctx, exchangestream.Frame{Type: exchangestream.Stop})
}

func (relay *Relay) write(ctx context.Context, frame exchangestream.Frame) error {
	encoded, err := exchangestream.Encode(frame)
	if err != nil {
		return err
	}
	return relay.stream.WriteFrame(ctx, encoded)
}
