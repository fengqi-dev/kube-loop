package reverserelay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/gateway/trafficlistener"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
)

var errClientStopped = errors.New("Exchange stopped by client")

type tcpRelayStream struct {
	connection net.Conn
	port       trafficmodel.Port
}

type udpRelayAssociation struct {
	connection *net.UDPConn
	remote     *net.UDPAddr
	port       trafficmodel.Port
	key        string
	lastSeen   time.Time
}

type relaySession struct {
	connection *websocket.Conn
	listeners  *trafficlistener.Listeners
	idle       time.Duration
	now        func() time.Time

	writeMu sync.Mutex
	nextID  atomic.Uint64
	mu      sync.Mutex
	tcp     map[uint64]*tcpRelayStream
	udp     map[uint64]*udpRelayAssociation
	udpKeys map[string]uint64
}

func newRelaySession(connection *websocket.Conn, listeners *trafficlistener.Listeners, idle time.Duration, now func() time.Time) *relaySession {
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

func (relay *relaySession) acceptTCP(ctx context.Context, binding trafficlistener.TCPBinding, streamWG *sync.WaitGroup) error {
	for {
		connection, err := binding.Listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		id := relay.nextStreamID()
		relay.mu.Lock()
		relay.tcp[id] = &tcpRelayStream{connection: connection, port: binding.Port}
		relay.mu.Unlock()
		if err := relay.write(ctx, exchangestream.Frame{
			Type: exchangestream.Open, StreamID: id, ServicePort: uint32(binding.Port.ServicePort), Protocol: exchangestream.ProtocolTCP,
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
			if writeErr := relay.write(ctx, exchangestream.Frame{Type: exchangestream.Data, StreamID: id, Payload: payload}); writeErr != nil {
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

func (relay *relaySession) readUDP(ctx context.Context, index int, binding trafficlistener.UDPBinding) error {
	buffer := make([]byte, 65507)
	for {
		count, remote, err := binding.Connection.ReadFromUDP(buffer)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		if count == 0 {
			continue
		}
		key := fmt.Sprintf("%d/%s", index, remote.String())
		id := relay.udpAssociation(binding, remote, key)
		payload := append([]byte(nil), buffer[:count]...)
		if err := relay.write(ctx, exchangestream.Frame{
			Type: exchangestream.Datagram, StreamID: id, ServicePort: uint32(binding.Port.ServicePort),
			Protocol: exchangestream.ProtocolUDP, Payload: payload,
		}); err != nil {
			return err
		}
	}
}

func (relay *relaySession) udpAssociation(binding trafficlistener.UDPBinding, remote *net.UDPAddr, key string) uint64 {
	now := relay.now().UTC()
	relay.mu.Lock()
	defer relay.mu.Unlock()
	if id, exists := relay.udpKeys[key]; exists {
		relay.udp[id].lastSeen = now
		return id
	}
	id := relay.nextStreamID()
	relay.udpKeys[key] = id
	relay.udp[id] = &udpRelayAssociation{
		connection: binding.Connection, remote: cloneUDPAddress(remote), port: binding.Port, key: key, lastSeen: now,
	}
	return id
}

func (relay *relaySession) reapUDP(ctx context.Context) error {
	interval := max(relay.idle/2, 50*time.Millisecond)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			cutoff := relay.now().UTC().Add(-relay.idle)
			var expired []uint64
			relay.mu.Lock()
			for id, association := range relay.udp {
				if !association.lastSeen.After(cutoff) {
					delete(relay.udp, id)
					delete(relay.udpKeys, association.key)
					expired = append(expired, id)
				}
			}
			relay.mu.Unlock()
			for _, id := range expired {
				if err := relay.write(ctx, exchangestream.Frame{Type: exchangestream.Close, StreamID: id}); err != nil {
					return err
				}
			}
		}
	}
}

func (relay *relaySession) readClient(ctx context.Context) error {
	for {
		messageType, encoded, err := relay.connection.Read(ctx)
		if err != nil {
			return err
		}
		if messageType != websocket.MessageBinary {
			return errors.New("Exchange stream requires binary frames")
		}
		frame, err := exchangestream.Decode(encoded)
		if err != nil {
			return err
		}
		switch frame.Type {
		case exchangestream.Data:
			stream := relay.tcpStream(frame.StreamID)
			if stream == nil {
				return errors.New("Exchange data references an unknown TCP stream")
			}
			if err := writeAll(stream.connection, frame.Payload); err != nil {
				relay.removeTCP(frame.StreamID)
				_ = relay.write(ctx, exchangestream.Frame{Type: exchangestream.Close, StreamID: frame.StreamID})
			}
		case exchangestream.CloseWrite:
			stream := relay.tcpStream(frame.StreamID)
			if stream == nil {
				return errors.New("Exchange half-close references an unknown TCP stream")
			}
			if connection, ok := stream.connection.(interface{ CloseWrite() error }); ok {
				_ = connection.CloseWrite()
			} else {
				_ = stream.connection.Close()
			}
		case exchangestream.Close:
			if !relay.removeStream(frame.StreamID) {
				return errors.New("Exchange close references an unknown stream")
			}
		case exchangestream.Datagram:
			association := relay.udpAssociationByID(frame.StreamID)
			if association == nil || frame.ServicePort != uint32(association.port.ServicePort) {
				return errors.New("Exchange datagram references an unknown UDP association")
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
	relay.writeMu.Lock()
	defer relay.writeMu.Unlock()
	return relay.connection.Write(ctx, websocket.MessageBinary, encoded)
}

func (relay *relaySession) tcpStream(id uint64) *tcpRelayStream {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	return relay.tcp[id]
}

func (relay *relaySession) udpAssociationByID(id uint64) *udpRelayAssociation {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	association := relay.udp[id]
	if association != nil {
		association.lastSeen = relay.now().UTC()
	}
	return association
}

func (relay *relaySession) removeTCP(id uint64) bool {
	relay.mu.Lock()
	stream := relay.tcp[id]
	delete(relay.tcp, id)
	relay.mu.Unlock()
	if stream == nil {
		return false
	}
	_ = stream.connection.Close()
	return true
}

func (relay *relaySession) removeStream(id uint64) bool {
	if relay.removeTCP(id) {
		return true
	}
	relay.mu.Lock()
	association := relay.udp[id]
	if association != nil {
		delete(relay.udp, id)
		delete(relay.udpKeys, association.key)
	}
	relay.mu.Unlock()
	return association != nil
}

func (relay *relaySession) closeStreams() {
	relay.mu.Lock()
	streams := make([]net.Conn, 0, len(relay.tcp))
	for _, stream := range relay.tcp {
		streams = append(streams, stream.connection)
	}
	relay.tcp = make(map[uint64]*tcpRelayStream)
	relay.udp = make(map[uint64]*udpRelayAssociation)
	relay.udpKeys = make(map[string]uint64)
	relay.mu.Unlock()
	for _, connection := range streams {
		_ = connection.Close()
	}
}

func (relay *relaySession) nextStreamID() uint64 {
	for {
		id := relay.nextID.Add(1)
		if id != 0 {
			return id
		}
	}
}

func cloneUDPAddress(address *net.UDPAddr) *net.UDPAddr {
	return &net.UDPAddr{IP: append(net.IP(nil), address.IP...), Port: address.Port, Zone: address.Zone}
}

func writeAll(connection net.Conn, payload []byte) error {
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
