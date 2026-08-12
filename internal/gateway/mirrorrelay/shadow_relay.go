package mirrorrelay

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/gateway/trafficlistener"
	"github.com/fengqi-dev/kube-loop/internal/protocol/mirrorstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
)

var errClientStopped = errors.New("Mirror stopped by client")

type tcpPrimaryStream struct {
	client  net.Conn
	primary net.Conn
}

type udpPrimaryAssociation struct {
	primary  net.Conn
	listener *net.UDPConn
	remote   *net.UDPAddr
	port     trafficmodel.Port
	key      string
	lastSeen time.Time
}

// mirrorRelay keeps the original backend as the synchronous response path.
// Shadow frames are offered to a bounded queue and are never awaited by a
// primary socket read/write loop.
type mirrorRelay struct {
	connection *websocket.Conn
	listeners  *trafficlistener.Listeners
	primaries  *primaryPool
	config     Config

	nextID atomic.Uint64
	shadow chan mirrorstream.Frame

	shadowMu      sync.Mutex
	droppedShadow map[uint64]struct{}

	mu      sync.Mutex
	tcp     map[uint64]*tcpPrimaryStream
	udp     map[uint64]*udpPrimaryAssociation
	udpKeys map[string]uint64
	streams sync.WaitGroup
}

func newMirrorRelay(
	connection *websocket.Conn,
	listeners *trafficlistener.Listeners,
	primaries *primaryPool,
	config Config,
) *mirrorRelay {
	return &mirrorRelay{
		connection: connection, listeners: listeners, primaries: primaries, config: config,
		shadow:        make(chan mirrorstream.Frame, config.ShadowQueueSize),
		droppedShadow: make(map[uint64]struct{}),
		tcp:           make(map[uint64]*tcpPrimaryStream), udp: make(map[uint64]*udpPrimaryAssociation),
		udpKeys: make(map[string]uint64),
	}
}

func (relay *mirrorRelay) run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 3+len(relay.listeners.TCP)+len(relay.listeners.UDP))
	var loops sync.WaitGroup
	start := func(run func() error) {
		loops.Go(func() {
			errCh <- run()
		})
	}
	start(func() error { return relay.readClient(ctx) })
	start(func() error { return relay.writeShadow(ctx) })
	for _, binding := range relay.listeners.TCP {
		start(func() error { return relay.acceptTCP(ctx, binding) })
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
	relay.closeStreams()
	loops.Wait()
	relay.streams.Wait()
	if result == nil && ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return result
}

func (relay *mirrorRelay) acceptTCP(ctx context.Context, binding trafficlistener.TCPBinding) error {
	for {
		client, err := binding.Listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		relay.streams.Go(func() {
			relay.serveTCP(ctx, binding.Port, client)
		})
	}
}

func (relay *mirrorRelay) serveTCP(ctx context.Context, port trafficmodel.Port, client net.Conn) {
	dialContext, cancel := context.WithTimeout(ctx, relay.config.PrimaryDialTimeout)
	primary, err := relay.primaries.Dial(dialContext, "tcp", port.ServicePort)
	cancel()
	if err != nil {
		_ = client.Close()
		return
	}
	id := relay.nextStreamID()
	relay.mu.Lock()
	relay.tcp[id] = &tcpPrimaryStream{client: client, primary: primary}
	relay.mu.Unlock()
	shadow := relay.emit(mirrorstream.Frame{
		Type: mirrorstream.Open, StreamID: id, ServicePort: uint32(port.ServicePort), Protocol: mirrorstream.ProtocolTCP,
	})

	requestDone := make(chan struct{})
	responseDone := make(chan struct{})
	var copies sync.WaitGroup
	copies.Add(2)
	go func() {
		defer copies.Done()
		defer close(requestDone)
		buffer := make([]byte, 32<<10)
		for {
			count, readErr := client.Read(buffer)
			if count > 0 {
				payload := buffer[:count]
				if writeErr := writePrimary(primary, payload); writeErr != nil {
					return
				}
				if shadow {
					shadow = relay.emit(mirrorstream.Frame{
						Type: mirrorstream.Data, StreamID: id, Payload: append([]byte(nil), payload...),
					})
				}
			}
			if readErr == nil {
				continue
			}
			if errors.Is(readErr, io.EOF) {
				closeWrite(primary)
				if shadow {
					relay.emit(mirrorstream.Frame{Type: mirrorstream.CloseWrite, StreamID: id})
				}
			}
			return
		}
	}()
	go func() {
		defer copies.Done()
		defer close(responseDone)
		_, _ = io.Copy(client, primary)
		closeWrite(client)
	}()

	select {
	case <-ctx.Done():
	case <-responseDone:
	case <-requestDone:
		select {
		case <-ctx.Done():
		case <-responseDone:
		}
	}
	relay.removeTCP(id)
	copies.Wait()
	relay.emit(mirrorstream.Frame{Type: mirrorstream.Close, StreamID: id})
	relay.clearDropped(id)
}

func (relay *mirrorRelay) readUDP(ctx context.Context, index int, binding trafficlistener.UDPBinding) error {
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
		key := primaryKey("udp", binding.Port.ServicePort) + "/" + remote.String() + "/" + strconv.Itoa(index)
		id, association, err := relay.udpAssociation(ctx, binding, remote, key)
		if err != nil {
			continue
		}
		payload := append([]byte(nil), buffer[:count]...)
		if err := writeDatagram(association.primary, payload); err != nil {
			relay.removeUDP(id)
			continue
		}
		relay.emit(mirrorstream.Frame{
			Type: mirrorstream.Datagram, StreamID: id,
			ServicePort: uint32(binding.Port.ServicePort), Protocol: mirrorstream.ProtocolUDP, Payload: payload,
		})
	}
}

func (relay *mirrorRelay) udpAssociation(
	ctx context.Context,
	binding trafficlistener.UDPBinding,
	remote *net.UDPAddr,
	key string,
) (uint64, *udpPrimaryAssociation, error) {
	now := relay.config.Now().UTC()
	relay.mu.Lock()
	if id, exists := relay.udpKeys[key]; exists {
		association := relay.udp[id]
		association.lastSeen = now
		relay.mu.Unlock()
		return id, association, nil
	}
	relay.mu.Unlock()
	dialContext, cancel := context.WithTimeout(ctx, relay.config.PrimaryDialTimeout)
	primary, err := relay.primaries.Dial(dialContext, "udp", binding.Port.ServicePort)
	cancel()
	if err != nil {
		return 0, nil, err
	}
	relay.mu.Lock()
	if id, exists := relay.udpKeys[key]; exists {
		association := relay.udp[id]
		association.lastSeen = now
		relay.mu.Unlock()
		_ = primary.Close()
		return id, association, nil
	}
	id := relay.nextStreamID()
	association := &udpPrimaryAssociation{
		primary: primary, listener: binding.Connection, remote: cloneUDPAddress(remote),
		port: binding.Port, key: key, lastSeen: now,
	}
	relay.udpKeys[key] = id
	relay.udp[id] = association
	relay.mu.Unlock()
	relay.streams.Go(func() {
		relay.readPrimaryUDP(ctx, id, association)
	})
	return id, association, nil
}

func (relay *mirrorRelay) readPrimaryUDP(ctx context.Context, id uint64, association *udpPrimaryAssociation) {
	buffer := make([]byte, 65507)
	for {
		count, err := association.primary.Read(buffer)
		if err != nil {
			if ctx.Err() == nil {
				relay.removeUDP(id)
			}
			return
		}
		if count == 0 {
			continue
		}
		if _, err := association.listener.WriteToUDP(buffer[:count], association.remote); err != nil {
			relay.removeUDP(id)
			return
		}
		relay.mu.Lock()
		if current := relay.udp[id]; current == association {
			association.lastSeen = relay.config.Now().UTC()
		}
		relay.mu.Unlock()
	}
}

func (relay *mirrorRelay) reapUDP(ctx context.Context) error {
	interval := max(relay.config.UDPIdleTimeout/2, 50*time.Millisecond)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			cutoff := relay.config.Now().UTC().Add(-relay.config.UDPIdleTimeout)
			var expired []uint64
			relay.mu.Lock()
			for id, association := range relay.udp {
				if !association.lastSeen.After(cutoff) {
					expired = append(expired, id)
				}
			}
			relay.mu.Unlock()
			for _, id := range expired {
				if relay.removeUDP(id) {
					relay.emit(mirrorstream.Frame{Type: mirrorstream.Close, StreamID: id})
					relay.clearDropped(id)
				}
			}
		}
	}
}

func (relay *mirrorRelay) readClient(ctx context.Context) error {
	for {
		messageType, encoded, err := relay.connection.Read(ctx)
		if err != nil {
			return err
		}
		if messageType != websocket.MessageBinary {
			return errors.New("Mirror stream requires binary frames")
		}
		frame, err := mirrorstream.Decode(encoded)
		if err != nil {
			return err
		}
		if frame.Type == mirrorstream.Stop {
			return errClientStopped
		}
		return errors.New("client sent a server-only Mirror frame")
	}
}

func (relay *mirrorRelay) writeShadow(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case frame := <-relay.shadow:
			writeContext, cancel := context.WithTimeout(ctx, relay.config.ShadowWriteTimeout)
			err := relay.writeFrame(writeContext, frame)
			cancel()
			if err != nil {
				return err
			}
		}
	}
}

func (relay *mirrorRelay) emit(frame mirrorstream.Frame) bool {
	if frame.StreamID != 0 {
		relay.shadowMu.Lock()
		_, dropped := relay.droppedShadow[frame.StreamID]
		if dropped {
			if frame.Type != mirrorstream.Close {
				relay.shadowMu.Unlock()
				return false
			}
			// A terminal frame gets one best-effort attempt after an earlier
			// overflow so the desktop can release any actor it already opened.
			delete(relay.droppedShadow, frame.StreamID)
		}
		select {
		case relay.shadow <- frame:
			relay.shadowMu.Unlock()
			return true
		default:
			relay.droppedShadow[frame.StreamID] = struct{}{}
			relay.shadowMu.Unlock()
			return false
		}
	}
	select {
	case relay.shadow <- frame:
		return true
	default:
		return false
	}
}

func (relay *mirrorRelay) writeFrame(ctx context.Context, frame mirrorstream.Frame) error {
	encoded, err := mirrorstream.Encode(frame)
	if err != nil {
		return err
	}
	return relay.connection.Write(ctx, websocket.MessageBinary, encoded)
}

func (relay *mirrorRelay) clearDropped(id uint64) {
	relay.shadowMu.Lock()
	delete(relay.droppedShadow, id)
	relay.shadowMu.Unlock()
}

func (relay *mirrorRelay) removeTCP(id uint64) bool {
	relay.mu.Lock()
	stream := relay.tcp[id]
	delete(relay.tcp, id)
	relay.mu.Unlock()
	if stream == nil {
		return false
	}
	_ = stream.client.Close()
	_ = stream.primary.Close()
	return true
}

func (relay *mirrorRelay) removeUDP(id uint64) bool {
	relay.mu.Lock()
	association := relay.udp[id]
	if association != nil {
		delete(relay.udp, id)
		delete(relay.udpKeys, association.key)
	}
	relay.mu.Unlock()
	if association == nil {
		return false
	}
	_ = association.primary.Close()
	return true
}

func (relay *mirrorRelay) closeStreams() {
	relay.mu.Lock()
	tcpStreams := make([]*tcpPrimaryStream, 0, len(relay.tcp))
	for _, stream := range relay.tcp {
		tcpStreams = append(tcpStreams, stream)
	}
	udpStreams := make([]*udpPrimaryAssociation, 0, len(relay.udp))
	for _, stream := range relay.udp {
		udpStreams = append(udpStreams, stream)
	}
	relay.tcp = make(map[uint64]*tcpPrimaryStream)
	relay.udp = make(map[uint64]*udpPrimaryAssociation)
	relay.udpKeys = make(map[string]uint64)
	relay.mu.Unlock()
	for _, stream := range tcpStreams {
		_ = stream.client.Close()
		_ = stream.primary.Close()
	}
	for _, stream := range udpStreams {
		_ = stream.primary.Close()
	}
}

func (relay *mirrorRelay) nextStreamID() uint64 {
	for {
		id := relay.nextID.Add(1)
		if id != 0 {
			return id
		}
	}
}

func writePrimary(connection net.Conn, payload []byte) error {
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

func writeDatagram(connection net.Conn, payload []byte) error {
	count, err := connection.Write(payload)
	if err != nil {
		return err
	}
	if count != len(payload) {
		return io.ErrShortWrite
	}
	return nil
}

func closeWrite(connection net.Conn) {
	if halfCloser, ok := connection.(interface{ CloseWrite() error }); ok {
		_ = halfCloser.CloseWrite()
	}
}

func cloneUDPAddress(address *net.UDPAddr) *net.UDPAddr {
	return &net.UDPAddr{IP: append(net.IP(nil), address.IP...), Port: address.Port, Zone: address.Zone}
}
