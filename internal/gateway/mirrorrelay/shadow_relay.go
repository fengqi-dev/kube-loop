package mirrorrelay

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/fengqi-dev/kube-loop/internal/gateway/trafficlistener"
	"github.com/fengqi-dev/kube-loop/internal/protocol/mirrorstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficstream"
)

var errClientStopped = errors.New("mirror stopped by client")

// mirrorRelay keeps the original backend as the synchronous response path.
// Shadow frames are offered to a bounded queue and are never awaited by a
// primary socket read/write loop.
type mirrorRelay struct {
	connection *trafficstream.FrameConn
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
	connection *trafficstream.FrameConn,
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

func (relay *mirrorRelay) readClient(ctx context.Context) error {
	encoded, err := relay.connection.ReadFrame(ctx)
	if err != nil {
		return err
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
	return relay.connection.WriteFrame(ctx, encoded)
}

func (relay *mirrorRelay) clearDropped(id uint64) {
	relay.shadowMu.Lock()
	delete(relay.droppedShadow, id)
	relay.shadowMu.Unlock()
}
