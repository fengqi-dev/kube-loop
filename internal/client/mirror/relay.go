package mirror

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/protocol/mirrorstream"
)

type shadowMessage struct {
	payload    []byte
	closeWrite bool
	finish     bool
}

const maxDroppedShadowStreams = 4096

// shadowActor owns one best-effort local shadow connection. Its queue and
// deadlines ensure a slow local process cannot block the WebSocket reader.
type shadowActor struct {
	target LocalTarget
	dial   DialContextFunc
	config Config
	queue  chan shadowMessage

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

func newShadowActor(
	parent context.Context,
	target LocalTarget,
	dial DialContextFunc,
	config Config,
) *shadowActor {
	ctx, cancel := context.WithCancel(parent)
	actor := &shadowActor{
		target: target, dial: dial, config: config,
		queue: make(chan shadowMessage, config.ShadowQueueSize),
		ctx:   ctx, cancel: cancel, done: make(chan struct{}),
	}
	go actor.run()
	return actor
}

func (actor *shadowActor) enqueue(message shadowMessage) bool {
	message.payload = append([]byte(nil), message.payload...)
	select {
	case <-actor.ctx.Done():
		return false
	default:
	}
	select {
	case actor.queue <- message:
		return true
	default:
		actor.cancel()
		return false
	}
}

func (actor *shadowActor) Close() {
	actor.cancel()
	<-actor.done
}

// Finish preserves the ordering of already queued request copies. A server
// Close frame describes the end of the primary stream; canceling immediately
// would race and discard Data frames that preceded it on the WebSocket.
func (actor *shadowActor) Finish() {
	actor.enqueue(shadowMessage{finish: true})
}

func (actor *shadowActor) run() {
	defer close(actor.done)
	defer actor.cancel()
	dialContext, dialCancel := context.WithTimeout(actor.ctx, actor.config.ShadowDialTimeout)
	connection, err := actor.dial(dialContext, actor.target.Protocol, localAddress(actor.target))
	dialCancel()
	if err != nil {
		actor.cancel()
		return
	}
	defer connection.Close()
	responseDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, connection)
		close(responseDone)
	}()
	idle := time.NewTimer(actor.config.ShadowIdleTimeout)
	defer idle.Stop()
	for {
		select {
		case <-actor.ctx.Done():
			return
		case <-responseDone:
			return
		case <-idle.C:
			return
		case message := <-actor.queue:
			if message.finish {
				return
			}
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(actor.config.ShadowIdleTimeout)
			if message.closeWrite {
				if connection, ok := connection.(interface{ CloseWrite() error }); ok {
					_ = connection.CloseWrite()
				}
				continue
			}
			if err := connection.SetWriteDeadline(time.Now().Add(actor.config.ShadowWriteTimeout)); err != nil {
				return
			}
			if actor.target.Protocol == "udp" {
				count, writeErr := connection.Write(message.payload)
				if writeErr != nil || count != len(message.payload) {
					return
				}
			} else if err := writeLocal(connection, message.payload); err != nil {
				return
			}
			_ = connection.SetWriteDeadline(time.Time{})
		}
	}
}

type localRelay struct {
	websocket *websocket.Conn
	targets   map[string]LocalTarget
	dial      DialContextFunc
	config    Config

	mu      sync.Mutex
	streams map[uint64]*shadowActor
	dropped map[uint64]struct{}
	wg      sync.WaitGroup
}

func newLocalRelay(
	connection *websocket.Conn,
	targets []LocalTarget,
	dial DialContextFunc,
	config Config,
) *localRelay {
	targetMap := make(map[string]LocalTarget, len(targets))
	for _, target := range targets {
		targetMap[targetKey(target.Protocol, target.ServicePort)] = target
	}
	return &localRelay{
		websocket: connection, targets: targetMap, dial: dial, config: config,
		streams: make(map[uint64]*shadowActor), dropped: make(map[uint64]struct{}),
	}
}

func (relay *localRelay) readReady(ctx context.Context) error {
	messageType, encoded, err := relay.websocket.Read(ctx)
	if err != nil {
		return err
	}
	if messageType != websocket.MessageBinary {
		return errors.New("Gateway returned a non-binary Mirror readiness frame")
	}
	frame, err := mirrorstream.Decode(encoded)
	if err != nil || frame.Type != mirrorstream.Ready {
		return errors.New("Gateway returned an invalid Mirror readiness frame")
	}
	return nil
}

func (relay *localRelay) run(ctx context.Context) error {
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
			return errors.New("Gateway sent a non-binary Mirror frame")
		}
		frame, err := mirrorstream.Decode(encoded)
		if err != nil {
			return err
		}
		switch frame.Type {
		case mirrorstream.Open:
			if _, err := relay.createActor(ctx, frame.StreamID, "tcp", int32(frame.ServicePort)); err != nil {
				return err
			}
		case mirrorstream.Data:
			actor, dropped := relay.actorState(frame.StreamID)
			if dropped {
				continue
			}
			if actor == nil || actor.target.Protocol != "tcp" {
				return errors.New("Gateway referenced an unknown local Mirror TCP stream")
			}
			if !actor.enqueue(shadowMessage{payload: frame.Payload}) {
				relay.drop(frame.StreamID, actor)
			}
		case mirrorstream.CloseWrite:
			actor, dropped := relay.actorState(frame.StreamID)
			if dropped {
				continue
			}
			if actor == nil || actor.target.Protocol != "tcp" {
				return errors.New("Gateway referenced an unknown local Mirror TCP stream")
			}
			if !actor.enqueue(shadowMessage{closeWrite: true}) {
				relay.drop(frame.StreamID, actor)
			}
		case mirrorstream.Datagram:
			actor, dropped := relay.actorState(frame.StreamID)
			if dropped {
				continue
			}
			if actor == nil {
				actor, err = relay.createActor(ctx, frame.StreamID, "udp", int32(frame.ServicePort))
				if err != nil {
					return err
				}
			}
			if actor.target.Protocol != "udp" || actor.target.ServicePort != int32(frame.ServicePort) {
				return errors.New("Gateway changed a local Mirror UDP target")
			}
			if !actor.enqueue(shadowMessage{payload: frame.Payload}) {
				relay.drop(frame.StreamID, actor)
			}
		case mirrorstream.Close:
			relay.remove(frame.StreamID)
		case mirrorstream.Stop:
			return nil
		case mirrorstream.Ready:
			return errors.New("Gateway sent duplicate Mirror readiness")
		default:
			return errors.New("Gateway sent a client-only Mirror frame")
		}
	}
}

func (relay *localRelay) stop(ctx context.Context) error {
	encoded, err := mirrorstream.Encode(mirrorstream.Frame{Type: mirrorstream.Stop})
	if err != nil {
		return err
	}
	return relay.websocket.Write(ctx, websocket.MessageBinary, encoded)
}

func (relay *localRelay) createActor(
	ctx context.Context,
	id uint64,
	protocol string,
	servicePort int32,
) (*shadowActor, error) {
	target, exists := relay.targets[targetKey(protocol, servicePort)]
	if !exists {
		return nil, errors.New("Gateway requested an unconfigured local Mirror target")
	}
	relay.mu.Lock()
	if relay.streams[id] != nil {
		relay.mu.Unlock()
		return nil, errors.New("Gateway reused an active Mirror stream ID")
	}
	if _, wasDropped := relay.dropped[id]; wasDropped {
		relay.mu.Unlock()
		return nil, errors.New("Gateway reused an active Mirror stream ID")
	}
	actor := newShadowActor(ctx, target, relay.dial, relay.config)
	relay.streams[id] = actor
	relay.wg.Add(1)
	relay.mu.Unlock()
	go func() {
		defer relay.wg.Done()
		<-actor.done
		relay.mu.Lock()
		if relay.streams[id] == actor {
			delete(relay.streams, id)
			relay.markDroppedLocked(id)
		}
		relay.mu.Unlock()
	}()
	return actor, nil
}

func (relay *localRelay) actorState(id uint64) (*shadowActor, bool) {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	_, dropped := relay.dropped[id]
	return relay.streams[id], dropped
}

func (relay *localRelay) drop(id uint64, actor *shadowActor) {
	relay.mu.Lock()
	if relay.streams[id] == actor {
		delete(relay.streams, id)
		relay.markDroppedLocked(id)
	}
	relay.mu.Unlock()
	actor.cancel()
}

func (relay *localRelay) markDroppedLocked(id uint64) {
	if len(relay.dropped) >= maxDroppedShadowStreams {
		for candidate := range relay.dropped {
			delete(relay.dropped, candidate)
			break
		}
	}
	relay.dropped[id] = struct{}{}
}

func (relay *localRelay) remove(id uint64) {
	relay.mu.Lock()
	actor := relay.streams[id]
	delete(relay.streams, id)
	delete(relay.dropped, id)
	relay.mu.Unlock()
	if actor != nil {
		actor.Finish()
	}
}

func (relay *localRelay) closeAll() {
	relay.mu.Lock()
	actors := make([]*shadowActor, 0, len(relay.streams))
	for _, actor := range relay.streams {
		actors = append(actors, actor)
	}
	relay.streams = make(map[uint64]*shadowActor)
	relay.dropped = make(map[uint64]struct{})
	relay.mu.Unlock()
	for _, actor := range actors {
		actor.Close()
	}
}

func localAddress(target LocalTarget) string {
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
