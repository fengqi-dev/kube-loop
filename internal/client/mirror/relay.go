package mirror

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficstream"
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
	responseDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, connection)
		close(responseDone)
	}()
	defer func() {
		_ = connection.Close()
		<-responseDone
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
			if actor.target.Protocol == mirrorProtocolUDP {
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
	stream  *trafficstream.FrameConn
	targets map[string]LocalTarget
	dial    DialContextFunc
	config  Config

	mu      sync.Mutex
	streams map[uint64]*shadowActor
	dropped map[uint64]struct{}
	wg      sync.WaitGroup
}
