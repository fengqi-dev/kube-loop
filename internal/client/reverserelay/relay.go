package reverserelay

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
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

type localConnection struct {
	connection net.Conn
	target     Target
}

// Relay translates the authenticated Exchange wire protocol into connections
// to the immutable local targets supplied by the desktop client.
type Relay struct {
	websocket *websocket.Conn
	targets   map[string]Target
	dial      DialContextFunc

	writeMu sync.Mutex
	mu      sync.Mutex
	tcp     map[uint64]*localConnection
	udp     map[uint64]*localConnection
	wg      sync.WaitGroup
}

func New(connection *websocket.Conn, targets []Target, dial DialContextFunc) *Relay {
	targetMap := make(map[string]Target, len(targets))
	for _, target := range targets {
		targetMap[targetKey(target.Protocol, target.ServicePort)] = target
	}
	return &Relay{
		websocket: connection, targets: targetMap, dial: dial,
		tcp: make(map[uint64]*localConnection), udp: make(map[uint64]*localConnection),
	}
}

func (relay *Relay) ReadReady(ctx context.Context) error {
	messageType, encoded, err := relay.websocket.Read(ctx)
	if err != nil {
		return err
	}
	if messageType != websocket.MessageBinary {
		return errors.New("Gateway returned a non-binary reverse readiness frame")
	}
	frame, err := exchangestream.Decode(encoded)
	if err != nil || frame.Type != exchangestream.Ready {
		return errors.New("Gateway returned an invalid reverse readiness frame")
	}
	return nil
}

func (relay *Relay) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		relay.closeAll()
		relay.wg.Wait()
		_ = relay.websocket.CloseNow()
	}()
	for {
		messageType, encoded, err := relay.websocket.Read(ctx)
		if err != nil {
			return err
		}
		if messageType != websocket.MessageBinary {
			return errors.New("Gateway sent a non-binary reverse frame")
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
				return errors.New("Gateway referenced an unknown local TCP stream")
			}
			if err := writeLocal(stream.connection, frame.Payload); err != nil {
				relay.remove(relay.tcp, frame.StreamID)
				_ = relay.write(ctx, exchangestream.Frame{Type: exchangestream.Close, StreamID: frame.StreamID})
			}
		case exchangestream.CloseWrite:
			stream := relay.connection(relay.tcp, frame.StreamID)
			if stream == nil {
				return errors.New("Gateway referenced an unknown local TCP stream")
			}
			if connection, ok := stream.connection.(interface{ CloseWrite() error }); ok {
				_ = connection.CloseWrite()
			} else {
				_ = stream.connection.Close()
			}
		case exchangestream.Close:
			if !relay.removeAny(frame.StreamID) {
				return errors.New("Gateway referenced an unknown local reverse stream")
			}
		case exchangestream.Datagram:
			if err := relay.datagram(ctx, frame); err != nil {
				return err
			}
		case exchangestream.Stop:
			return nil
		case exchangestream.Ready:
			return errors.New("Gateway sent duplicate reverse readiness")
		default:
			return errors.New("Gateway sent a client-only reverse frame")
		}
	}
}

func (relay *Relay) Stop(ctx context.Context) error {
	return relay.write(ctx, exchangestream.Frame{Type: exchangestream.Stop})
}

func (relay *Relay) openTCP(ctx context.Context, frame exchangestream.Frame) error {
	target, exists := relay.targets[targetKey("tcp", int32(frame.ServicePort))]
	if !exists {
		return errors.New("Gateway requested an unconfigured local TCP target")
	}
	if relay.hasStream(frame.StreamID) {
		return errors.New("Gateway reused an active reverse stream ID")
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

func (relay *Relay) datagram(ctx context.Context, frame exchangestream.Frame) error {
	target, exists := relay.targets[targetKey("udp", int32(frame.ServicePort))]
	if !exists {
		return errors.New("Gateway requested an unconfigured local UDP target")
	}
	association := relay.connection(relay.udp, frame.StreamID)
	if association == nil {
		if relay.hasStream(frame.StreamID) {
			return errors.New("Gateway reused an active reverse stream ID")
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
	if association.target.ServicePort != int32(frame.ServicePort) {
		return errors.New("Gateway changed a reverse UDP association port")
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
			ServicePort: uint32(association.target.ServicePort), Protocol: exchangestream.ProtocolUDP,
			Payload: append([]byte(nil), buffer[:count]...),
		}); err != nil {
			relay.remove(relay.udp, id)
			return
		}
	}
}

func (relay *Relay) write(ctx context.Context, frame exchangestream.Frame) error {
	encoded, err := exchangestream.Encode(frame)
	if err != nil {
		return err
	}
	relay.writeMu.Lock()
	defer relay.writeMu.Unlock()
	return relay.websocket.Write(ctx, websocket.MessageBinary, encoded)
}

func (relay *Relay) connection(items map[uint64]*localConnection, id uint64) *localConnection {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	return items[id]
}

func (relay *Relay) hasStream(id uint64) bool {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	return relay.tcp[id] != nil || relay.udp[id] != nil
}

func (relay *Relay) remove(items map[uint64]*localConnection, id uint64) bool {
	relay.mu.Lock()
	stream := items[id]
	delete(items, id)
	relay.mu.Unlock()
	if stream == nil {
		return false
	}
	_ = stream.connection.Close()
	return true
}

func (relay *Relay) removeAny(id uint64) bool {
	if relay.remove(relay.tcp, id) {
		return true
	}
	return relay.remove(relay.udp, id)
}

func (relay *Relay) closeAll() {
	relay.mu.Lock()
	connections := make([]net.Conn, 0, len(relay.tcp)+len(relay.udp))
	for _, stream := range relay.tcp {
		connections = append(connections, stream.connection)
	}
	for _, stream := range relay.udp {
		connections = append(connections, stream.connection)
	}
	relay.tcp = make(map[uint64]*localConnection)
	relay.udp = make(map[uint64]*localConnection)
	relay.mu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}
}

func targetKey(protocol string, port int32) string {
	return strings.ToLower(protocol) + "/" + strconv.Itoa(int(port))
}

func localAddress(target Target) string {
	return net.JoinHostPort(target.LocalHost, strconv.Itoa(int(target.LocalPort)))
}

func writeLocal(connection net.Conn, payload []byte) error {
	for len(payload) > 0 {
		count, err := connection.Write(payload)
		if err != nil {
			return err
		}
		if count == 0 {
			return io.ErrShortWrite
		}
		payload = payload[count:]
	}
	return nil
}
